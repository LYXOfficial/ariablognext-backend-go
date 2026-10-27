package platform

import (
	"fmt"
	"html"
	"strings"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
)

// 邮件模板：逐字复刻旧后端 flink/mail.py 的 HTML 结构与配色
// （粉色渐变头部、白色圆角卡片、友链卡片式信息块、胶囊按钮、虚线页脚）。
// 改动样式必须与旧版视觉逐项对齐——用户明确反馈过「样式和原后端不一致」。
const (
	colorTheme      = "#ef859d"
	colorThemeLight = "#f79dbc"
	colorTitle      = "#c5343e"
	colorText       = "#4c4948"
	colorMuted      = "#8a8a8a"
	colorBG         = "#f7f9fe"
	colorCardBorder = "#f2e4e9"
	fontStack       = `-apple-system,BlinkMacSystemFont,"PingFang SC","Microsoft YaHei","Segoe UI",Roboto,"Helvetica Neue",Arial,sans-serif`
)

// MailTemplates 生成友链申请相关的四类邮件。
type MailTemplates struct {
	site  config.SiteConfig
	flink config.FlinkConfig
}

func NewMailTemplates(site config.SiteConfig, flink config.FlinkConfig) *MailTemplates {
	return &MailTemplates{site: site, flink: flink}
}

func esc(s string) string { return html.EscapeString(s) }

func (t *MailTemplates) linksURL() string {
	return strings.TrimRight(t.site.URL, "/") + "/links"
}

// signature 是纯文本邮件末尾的落款。
func (t *MailTemplates) signature() string {
	return fmt.Sprintf("\n\n—— 本邮件由 %s 自动发送，请勿直接回复。\n%s/links",
		t.site.Name, strings.TrimRight(t.site.URL, "/"))
}

// paragraph 对应旧 _paragraph。
func paragraph(htmlText string) string {
	return fmt.Sprintf(`<p style="margin:0 0 14px;font-size:14px;line-height:1.9;color:%s;">%s</p>`,
		colorText, htmlText)
}

// siteCard 对应旧 _site_card：仿博客友链卡片（头像 + 站名 + 网址 + 描述）。
func siteCard(name, url, avatar, descr, extra string) string {
	avatarHTML := ""
	if avatar != "" {
		avatarHTML = fmt.Sprintf(`<img src="%s" width="42" height="42" alt="" style="display:block;width:42px;height:42px;border-radius:50%%;object-fit:cover;background:#f1f1f1;" />`,
			esc(avatar))
	}
	descrHTML := ""
	if descr != "" {
		descrHTML = fmt.Sprintf(`<div style="margin-top:5px;font-size:12.5px;line-height:1.6;color:%s;">%s</div>`,
			colorMuted, esc(descr))
	}
	return fmt.Sprintf(`
  <table role="presentation" width="100%%" cellpadding="0" cellspacing="0"
         style="border-collapse:separate;margin:4px 0 20px;">
    <tr>
      <td style="background:%s;border-radius:14px;padding:14px 16px;">
        <table role="presentation" width="100%%" cellpadding="0" cellspacing="0">
          <tr>
            <td width="54" valign="top">%s</td>
            <td valign="top" style="padding-left:12px;">
              <div style="font-size:15px;font-weight:600;color:%s;">
                %s
              </div>
              <div style="margin-top:5px;font-size:12.5px;word-break:break-all;">
                <a href="%s" style="color:%s;text-decoration:none;">%s</a>
              </div>
              %s
              %s
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>`,
		colorBG, avatarHTML, colorText, esc(name),
		esc(url), colorTheme, esc(url),
		descrHTML, extra)
}

// noteBlock 对应旧 _note_block（浅粉底 + 左侧主题色竖线）。
func noteBlock(htmlText string) string {
	return fmt.Sprintf(`
  <table role="presentation" width="100%%" cellpadding="0" cellspacing="0"
         style="border-collapse:separate;margin:4px 0 20px;">
    <tr>
      <td style="background:#fff5f7;border-left:4px solid %s;
                 border-radius:10px;padding:12px 14px;font-size:13.5px;
                 line-height:1.8;color:%s;">
        %s
      </td>
    </tr>
  </table>`, colorTheme, colorText, htmlText)
}

// button 对应旧 _button（胶囊按钮）。
func button(text, url string) string {
	if url == "" {
		return ""
	}
	return fmt.Sprintf(`
  <div style="margin:22px 0 6px;">
    <a href="%s"
       style="display:inline-block;padding:11px 26px;background:%s;
              color:#ffffff;font-size:14px;border-radius:22px;
              text-decoration:none;font-weight:600;">%s</a>
  </div>`, esc(url), colorTheme, esc(text))
}

// renderHTML 对应旧 _render_html：整封信的外壳。
// %% 转义 fmt 的百分号，URL/文案全部走参数，避免正文里的 % 破坏格式串。
func (t *MailTemplates) renderHTML(title string, paragraphs, blocks []string, ctaText, ctaURL string) string {
	body := strings.Join(paragraphs, "") + strings.Join(blocks, "") + button(ctaText, ctaURL)
	siteName := esc(t.site.Name)
	siteURL := esc(strings.TrimRight(t.site.URL, "/"))
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width,initial-scale=1" />
  <meta name="color-scheme" content="light" />
  <title>%s</title>
</head>
<body style="margin:0;padding:0;background:%s;font-family:%s;">
  <div style="padding:28px 14px;">
    <table role="presentation" width="100%%" cellpadding="0" cellspacing="0"
           style="border-collapse:separate;max-width:560px;margin:0 auto;">
      <tr>
        <td style="background:#ffffff;border:1px solid %s;
                   border-radius:18px;overflow:hidden;">
          <!-- 头部：粉色渐变，呼应博客主题 -->
          <table role="presentation" width="100%%" cellpadding="0" cellspacing="0">
            <tr>
              <td style="background:%s;
                         background-image:linear-gradient(135deg,%s,%s);
                         padding:20px 24px;">
                <div style="font-size:17px;font-weight:700;color:#ffffff;">
                  %s
                </div>
                <div style="margin-top:4px;font-size:12px;color:#ffffffd0;">
                  友链申请通知
                </div>
              </td>
            </tr>
          </table>

          <!-- 正文 -->
          <table role="presentation" width="100%%" cellpadding="0" cellspacing="0">
            <tr>
              <td style="padding:24px 24px 18px;">
                <div style="font-size:16.5px;font-weight:700;color:%s;
                            margin-bottom:16px;">%s</div>
                %s
              </td>
            </tr>
          </table>

          <!-- 页脚 -->
          <table role="presentation" width="100%%" cellpadding="0" cellspacing="0">
            <tr>
              <td style="padding:14px 24px 18px;border-top:1px dashed %s;">
                <div style="font-size:11.5px;line-height:1.7;color:%s;">
                  本邮件由 <a href="%s"
                    style="color:%s;text-decoration:none;">%s</a>
                  自动发送，请勿直接回复。
                </div>
              </td>
            </tr>
          </table>
        </td>
      </tr>
    </table>
  </div>
</body>
</html>`,
		esc(title), colorBG, fontStack, colorCardBorder,
		colorThemeLight, colorThemeLight, colorTheme, siteName,
		colorTitle, esc(title), body,
		colorCardBorder, colorMuted, siteURL, colorTheme, siteName)
}

// ApplicationReceived：申请人回执（旧 application_received）。
func (t *MailTemplates) ApplicationReceived(name, link string, isModify bool, avatar, descr string) (subject, text, html string) {
	kind := "友链"
	if isModify {
		kind = "修改"
	}
	subject = fmt.Sprintf("已收到你的%s申请 - %s", kind, name)
	text = fmt.Sprintf("你好，%s：\n\n我们已收到你对 %s 的%s申请，博主会尽快审核。\n\n"+
		"审核结果会发送到这个邮箱；你也可以在友链页面用该邮箱自助查询进度。\n"+
		"如果审核未通过，不会单独通知，也不会保留申请记录，可以修改后重新提交。",
		name, link, kind) + t.signature()
	html = t.renderHTML(
		fmt.Sprintf("已收到你的%s申请", kind),
		[]string{
			paragraph(fmt.Sprintf("你好，<b>%s</b>：", esc(name))),
			paragraph(fmt.Sprintf("我们已收到你的%s申请，博主会尽快审核。", kind)),
		},
		[]string{
			siteCard(name, link, avatar, descr, ""),
			noteBlock("审核结果会发送到这个邮箱，也可以在友链页面用该邮箱自助查询进度。"),
		},
		"前往友链页面", t.linksURL(),
	)
	return
}

// ApplicationApproved：审核通过（旧 application_approved）。
func (t *MailTemplates) ApplicationApproved(name, link, group string, isModify bool, avatar, descr string) (subject, text, html string) {
	var title, intro string
	if isModify {
		subject = fmt.Sprintf("友链修改已生效 - %s", name)
		text = fmt.Sprintf("你好，%s：\n\n你对 %s 的友链修改申请已通过，新的信息已经生效。\n\n"+
			"若显示有延迟，刷新友链页面即可。感谢你的支持！", name, link) + t.signature()
		title = "友链修改已生效"
		intro = "你的友链修改申请已通过，新的信息已经生效。"
	} else {
		subject = fmt.Sprintf("友链申请已通过 - %s", name)
		text = fmt.Sprintf("你好，%s：\n\n你的友链申请已通过，%s 已添加到「%s」分组。\n\n"+
			"若显示有延迟，刷新友链页面即可。感谢你的支持！", name, link, group) + t.signature()
		title = "友链申请已通过"
		intro = fmt.Sprintf("你的友链申请已通过，已添加到「%s」分组。", esc(group))
	}
	html = t.renderHTML(title,
		[]string{
			paragraph(fmt.Sprintf("你好，<b>%s</b>：", esc(name))),
			paragraph(intro),
		},
		[]string{siteCard(name, link, avatar, descr, "")},
		"前往友链页面", t.linksURL(),
	)
	return
}

// ApplicationRejected：审核未通过（旧 application_rejected；reason 非空才发）。
func (t *MailTemplates) ApplicationRejected(name, link, reason, avatar string) (subject, text, html string) {
	subject = fmt.Sprintf("友链申请未通过 - %s", name)
	text = fmt.Sprintf("你好，%s：\n\n很抱歉，你对 %s 的友链申请未能通过审核。\n\n原因：%s\n\n"+
		"你可以按上述说明调整后重新提交申请。", name, link, reason) + t.signature()
	html = t.renderHTML("友链申请未通过",
		[]string{
			paragraph(fmt.Sprintf("你好，<b>%s</b>：", esc(name))),
			paragraph("很抱歉，你的友链申请未能通过审核。"),
		},
		[]string{
			siteCard(name, link, avatar, "", ""),
			noteBlock(fmt.Sprintf("<b>原因：</b>%s", esc(reason))),
			paragraph("你可以按上述说明调整后重新提交申请。"),
		},
		"重新提交申请", t.linksURL(),
	)
	return
}

// AdminNewApplication：博主提醒（旧 admin_new_application）。
func (t *MailTemplates) AdminNewApplication(name, link, email string, isModify bool, avatar, descr string) (subject, text, html string) {
	kind := "新增申请"
	if isModify {
		kind = "修改申请"
	}
	subject = fmt.Sprintf("[友链] 新的%s：%s", kind, name)
	text = fmt.Sprintf("收到一条%s：\n\n  站点名称：%s\n  网址：%s\n  申请人邮箱：%s\n\n"+
		"请到后台「友链 → 待审核申请」处理。", kind, name, link, email) + t.signature()

	extra := fmt.Sprintf(`<div style="margin-top:5px;font-size:12.5px;color:%s;">申请人邮箱：%s</div>`,
		colorMuted, esc(email))
	adminURL := strings.TrimRight(t.site.AdminPanelURL, "/")
	ctaText, ctaURL := "前往友链页面", t.linksURL()
	if adminURL != "" {
		ctaText, ctaURL = "去后台处理", adminURL
	}
	html = t.renderHTML(fmt.Sprintf("新的%s", kind),
		[]string{paragraph(fmt.Sprintf("收到一条%s，请到后台处理。", kind))},
		[]string{siteCard(name, link, avatar, descr, extra)},
		ctaText, ctaURL,
	)
	return
}
