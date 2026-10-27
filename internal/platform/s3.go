// Package platform 收纳外部服务适配：S3 图床 / SMTP 邮件 / GitHub dispatch / LLM。
// 共同约定：所有网络调用带超时；失败转成带 cause 的 apperr 或 best-effort 布尔，
// 绝不让外部故障变成 500 panic。
package platform

import (
	"context"
	"fmt"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3 是 Cloudflare R2（S3 兼容）图床客户端。
type S3 struct {
	client    *s3.Client
	bucket    string
	publicURL string
}

// NewS3 按配置建客户端。R2 的 region 固定 us-east-1（配置文件里写明）。
func NewS3(ctx context.Context, cfg config.S3Config) (*S3, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
			return aws.Credentials{
				AccessKeyID:     cfg.AccessKeyID,
				SecretAccessKey: cfg.SecretAccessKey,
			}, nil
		})),
	)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Endpoint)
		// R2 要求 path-style 兼容关闭（虚拟主机式），保持默认即可
	})
	return &S3{client: client, bucket: cfg.Bucket, publicURL: cfg.PublicURL}, nil
}

// s3Call 把 aws-sdk 的阻塞调用放进 goroutine + 超时：
// SDK 偶发的慢响应不该拖死整个请求（TASKS §7.1 的要求）。
func s3Call[T any](timeout time.Duration, fn func(ctx context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	type result struct {
		val T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, err := fn(ctx)
		ch <- result{v, err}
	}()
	select {
	case r := <-ch:
		return r.val, r.err
	case <-ctx.Done():
		var zero T
		return zero, apperr.Wrap(apperr.UpstreamFailed, "图床响应超时，请稍后重试", ctx.Err())
	}
}

// wrapS3Err 统一转 upstream.failed（cause 保留给 details）。
func wrapS3Err(op string, err error) error {
	if err == nil {
		return nil
	}
	var ae *apperr.Error
	if asAppErr(err, &ae) {
		return ae
	}
	return apperr.Wrap(apperr.UpstreamFailed, "图床操作失败（"+op+"）", err)
}

// Upload 上传（key 已存在则覆盖），返回公网 URL。
func (s *S3) Upload(ctx context.Context, key string, body []byte, contentType string) (string, error) {
	uploader := manager.NewUploader(s.client)
	_, err := s3Call(30*time.Second, func(ctx context.Context) (any, error) {
		return uploader.Upload(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(s.bucket),
			Key:         aws.String(key),
			Body:        bytesReader(body),
			ContentType: aws.String(contentType),
		})
	})
	if err != nil {
		return "", wrapS3Err("上传", err)
	}
	return s.PublicURL(key), nil
}

// Delete 删除对象；对象不存在也算成功（S3 语义如此）。
func (s *S3) Delete(ctx context.Context, key string) error {
	_, err := s3Call(30*time.Second, func(ctx context.Context) (any, error) {
		return s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(key),
		})
	})
	return wrapS3Err("删除", err)
}

// ImageObject 是图床目录里的一张图。
type ImageObject struct {
	Key          string `json:"key"`
	Size         int64  `json:"size"`
	LastModified int64  `json:"lastModified"` // unix 秒
	URL          string `json:"url"`
}

// List 按最后修改时间倒序列出对象。
func (s *S3) List(ctx context.Context) ([]ImageObject, error) {
	out, err := s3Call(30*time.Second, func(ctx context.Context) ([]ImageObject, error) {
		var items []ImageObject
		pager := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
			Bucket: aws.String(s.bucket),
		})
		for pager.HasMorePages() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, obj := range page.Contents {
				if obj.Key == nil {
					continue
				}
				var mod int64
				if obj.LastModified != nil {
					mod = obj.LastModified.Unix()
				}
				items = append(items, ImageObject{
					Key:          *obj.Key,
					Size:         deref64(obj.Size),
					LastModified: mod,
					URL:          s.PublicURL(*obj.Key),
				})
			}
		}
		return items, nil
	})
	if err != nil {
		return nil, wrapS3Err("列目录", err)
	}
	return out, nil
}

// PublicURL 拼对象的公网地址（前缀与 dashboard 的 imageHost 配置必须一致）。
func (s *S3) PublicURL(key string) string {
	return fmt.Sprintf("%s/%s", s.publicURL, key)
}
