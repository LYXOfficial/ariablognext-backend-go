package platform

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
)

// Mailer 是 SMTP 客户端：隐式 TLS(465) 与 STARTTLS(587) 两种模式，
// 规则照抄旧后端 flink/mail.py：secure 显式指定优先，否则按端口推断（465=SSL）。
// 发送失败是 best-effort：返回 false + 日志，绝不影响申请主流程。
type Mailer struct {
	cfg config.MailConfig
}

func NewMailer(cfg config.MailConfig) *Mailer {
	return &Mailer{cfg: cfg}
}

// Configured 判断 SMTP 是否可用（host/user/pass 三者齐全）。
func (m *Mailer) Configured() bool {
	return m.cfg.Host != "" && m.cfg.User != "" && m.cfg.Pass != ""
}

func (m *Mailer) port() int {
	if m.cfg.Port == "" {
		return 465
	}
	p, err := strconv.Atoi(m.cfg.Port)
	if err != nil || p <= 0 {
		return 465
	}
	return p
}

// useSSL / useStartTLS 与旧后端推断规则一致。
func (m *Mailer) useSSL() bool {
	switch strings.ToLower(m.cfg.Secure) {
	case "true", "ssl", "1", "yes":
		return true
	case "starttls", "tls", "false":
		return false
	default:
		return m.port() == 465
	}
}

func (m *Mailer) sender() string {
	if m.cfg.From != "" {
		return m.cfg.From
	}
	return m.cfg.User
}

// Send 发送纯文本 + HTML 双版本邮件。成功返回 true；失败记日志返回 false。
// 12 秒超时（与旧后端 TIMEOUT_SECONDS 一致）。
func (m *Mailer) Send(to, subject, text, html string) bool {
	if to == "" || !m.Configured() {
		return false
	}
	done := make(chan error, 1)
	go func() {
		done <- m.send(to, subject, text, html)
	}()
	select {
	case err := <-done:
		if err != nil {
			slogWarn("邮件发送失败（不影响主流程）", "to", to, "err", err)
			return false
		}
		return true
	case <-time.After(12 * time.Second):
		slogWarn("邮件发送超时（不影响主流程）", "to", to)
		return false
	}
}

func (m *Mailer) send(to, subject, text, html string) error {
	addr := m.cfg.Host + ":" + strconv.Itoa(m.port())
	from := m.sender()
	msg := buildMIME(from, m.cfg.FromName, to, subject, text, html)

	conn, err := net.DialTimeout("tcp", addr, 8*time.Second)
	if err != nil {
		return fmt.Errorf("连接 SMTP 失败: %w", err)
	}
	// TLS 握手与命令交互的 deadline 统一在 conn 上控制
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	var client *smtp.Client
	if m.useSSL() {
		host, _, _ := net.SplitHostPort(addr)
		client, err = smtp.NewClient(tls.Client(conn, &tls.Config{ServerName: host}), host)
	} else {
		client, err = smtp.NewClient(conn, m.cfg.Host)
	}
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("建立 SMTP 会话失败: %w", err)
	}
	defer client.Close()

	if !m.useSSL() {
		// STARTTLS：服务器支持才升级，不支持就明文（仅本地调试用）
		if ok, _ := client.Extension("STARTTLS"); ok {
			host, _, _ := net.SplitHostPort(addr)
			if err := client.StartTLS(&tls.Config{ServerName: host}); err != nil {
				return fmt.Errorf("STARTTLS 失败: %w", err)
			}
		}
	}

	// 只处理 PLAIN/LOGIN（主流邮箱都支持其中之一）
	if ok, _ := client.Extension("AUTH"); ok {
		if err := client.Auth(smtp.PlainAuth("", m.cfg.User, m.cfg.Pass, m.cfg.Host)); err != nil {
			return fmt.Errorf("SMTP 认证失败: %w", err)
		}
	}

	if err := client.Mail(from); err != nil {
		return fmt.Errorf("设置发件人失败: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("设置收件人失败: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}
