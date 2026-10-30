// Mailer sends transactional emails via SMTP with explicit STARTTLS.
// Works with Gmail App Passwords, SendGrid, Mailgun, etc.

package email

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"strings"
	"time"
)

// Config holds SMTP settings resolved from environment variables.
type Config struct {
	Host string
	Port string
	User string
	Pass string
	From string
}

func loadConfig() (*Config, error) {
	cfg := &Config{
		Host: os.Getenv("SMTP_HOST"),
		Port: os.Getenv("SMTP_PORT"),
		User: os.Getenv("SMTP_USER"),
		Pass: os.Getenv("SMTP_PASS"),
		From: os.Getenv("SMTP_FROM"),
	}
	if cfg.Host == "" {
		return nil, fmt.Errorf("SMTP_HOST is not set")
	}
	if cfg.User == "" {
		return nil, fmt.Errorf("SMTP_USER is not set")
	}
	if cfg.Pass == "" {
		return nil, fmt.Errorf("SMTP_PASS is not set")
	}
	if cfg.Port == "" {
		cfg.Port = "587"
	}
	if cfg.From == "" {
		cfg.From = cfg.User
	}
	return cfg, nil
}

// SendVerificationCode sends a 6-digit OTP to confirm email ownership during signup.
func SendVerificationCode(toEmail, toName, code, verifyURL string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("SMTP config error: %w", err)
	}

	subject := "Your FoxVue verification code"
	body := buildVerificationEmail(toName, code, verifyURL)
	msg := buildMIME(cfg.From, toEmail, subject, body)

	return sendWithSTARTTLS(cfg, toEmail, msg)
}

// SendWelcome sends a welcome email after account creation.
func SendWelcome(toEmail, toName, loginURL string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("SMTP config error: %w", err)
	}

	subject := "Welcome to FoxVue — you're all set"
	body := buildWelcomeEmail(toName, loginURL)
	msg := buildMIME(cfg.From, toEmail, subject, body)

	return sendWithSTARTTLS(cfg, toEmail, msg)
}

// SendPasswordReset sends a password reset email.
// Returns a descriptive error so callers can log exactly what went wrong.
func SendPasswordReset(toEmail, toName, resetURL string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("SMTP config error: %w", err)
	}

	subject := "Reset your FoxVue password"
	body := buildResetEmail(toName, resetURL)
	msg := buildMIME(cfg.From, toEmail, subject, body)

	return sendWithSTARTTLS(cfg, toEmail, msg)
}

// sendWithSTARTTLS connects on port 587 and upgrades to TLS via STARTTLS.
// This is the correct method for Gmail and most modern SMTP providers.
func sendWithSTARTTLS(cfg *Config, to, msg string) error {
	addr := net.JoinHostPort(cfg.Host, cfg.Port)

	// Dial plain TCP first (port 587 starts unencrypted)
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("TCP dial to %s failed: %w", addr, err)
	}

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return fmt.Errorf("SMTP client creation failed: %w", err)
	}
	defer func() { _ = client.Close() }()

	// Upgrade to TLS via STARTTLS
	tlsCfg := &tls.Config{
		ServerName: cfg.Host,
		MinVersion: tls.VersionTLS12,
	}
	if err := client.StartTLS(tlsCfg); err != nil {
		return fmt.Errorf("STARTTLS failed: %w", err)
	}

	// Authenticate
	auth := smtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Host)
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("SMTP auth failed (check App Password): %w", err)
	}

	// Set sender
	if err := client.Mail(cfg.From); err != nil {
		return fmt.Errorf("MAIL FROM failed: %w", err)
	}

	// Set recipient
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO failed: %w", err)
	}

	// Write message body
	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA command failed: %w", err)
	}
	if _, err := fmt.Fprint(wc, msg); err != nil {
		return fmt.Errorf("writing message body failed: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("closing DATA writer failed: %w", err)
	}

	return client.Quit()
}

func buildMIME(from, to, subject, htmlBody string) string {
	var sb strings.Builder
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	fmt.Fprintf(&sb, "From: FoxVue <%s>\r\n", from)
	fmt.Fprintf(&sb, "To: %s\r\n", to)
	fmt.Fprintf(&sb, "Subject: %s\r\n", subject)
	sb.WriteString("\r\n")
	sb.WriteString(htmlBody)
	return sb.String()
}

func buildResetEmail(name, resetURL string) string {
	if name == "" {
		name = "there"
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Reset your password</title>
</head>
<body style="margin:0;padding:0;background:#0a0f1a;font-family:system-ui,sans-serif;">
  <table width="100%%" cellpadding="0" cellspacing="0" style="background:#0a0f1a;padding:40px 20px;">
    <tr><td align="center">
      <table width="100%%" cellpadding="0" cellspacing="0" style="max-width:520px;background:#111827;border-radius:16px;border:1px solid #1e2d3d;overflow:hidden;">
        <tr><td style="height:4px;background:linear-gradient(90deg,#0ea5e9,#38bdf8,#7dd3fc);"></td></tr>
        <tr><td style="padding:36px 40px;">
          <h1 style="margin:0 0 4px;font-size:22px;font-weight:700;color:#f1f5f9;">FoxVue</h1>
          <p style="margin:0 0 28px;font-size:13px;color:#64748b;">AI-powered interview practice</p>
          <h2 style="margin:0 0 12px;font-size:18px;font-weight:600;color:#f1f5f9;">Reset your password</h2>
          <p style="margin:0 0 24px;font-size:15px;color:#94a3b8;line-height:1.6;">
            Hi %s, we received a request to reset your password. Click the button below — this link expires in <strong style="color:#f1f5f9;">30 minutes</strong>.
          </p>
          <a href="%s" style="display:inline-block;padding:14px 32px;background:#38bdf8;color:#0a0f1a;font-weight:700;font-size:15px;border-radius:10px;text-decoration:none;">
            Reset Password
          </a>
          <p style="margin:24px 0 0;font-size:13px;color:#64748b;line-height:1.6;">
            If you didn't request this, you can safely ignore this email.
          </p>
          <p style="margin:12px 0 0;font-size:12px;color:#475569;">
            Or copy this link:<br><span style="color:#38bdf8;word-break:break-all;">%s</span>
          </p>
        </td></tr>
        <tr><td style="padding:20px 40px;border-top:1px solid #1e2d3d;">
          <p style="margin:0;font-size:12px;color:#475569;text-align:center;">
            &copy; 2025 FoxVue. Do not reply to this email.
          </p>
        </td></tr>
      </table>
    </td></tr>
  </table>
</body>
</html>`, name, resetURL, resetURL)
}

func buildWelcomeEmail(name, loginURL string) string {
	if name == "" {
		name = "there"
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Welcome to FoxVue</title>
</head>
<body style="margin:0;padding:0;background:#0a0f1a;font-family:system-ui,sans-serif;">
  <table width="100%%" cellpadding="0" cellspacing="0" style="background:#0a0f1a;padding:40px 20px;">
    <tr><td align="center">
      <table width="100%%" cellpadding="0" cellspacing="0" style="max-width:520px;background:#111827;border-radius:16px;border:1px solid #1e2d3d;overflow:hidden;">
        <tr><td style="height:4px;background:linear-gradient(90deg,#0ea5e9,#38bdf8,#7dd3fc);"></td></tr>
        <tr><td style="padding:36px 40px;">
          <h1 style="margin:0 0 4px;font-size:22px;font-weight:700;color:#f1f5f9;">FoxVue</h1>
          <p style="margin:0 0 28px;font-size:13px;color:#64748b;">AI-powered interview practice</p>
          <h2 style="margin:0 0 12px;font-size:18px;font-weight:600;color:#f1f5f9;">Welcome, %s!</h2>
          <p style="margin:0 0 24px;font-size:15px;color:#94a3b8;line-height:1.6;">
            Your account has been created successfully. Click the button below to sign in and start practising.
          </p>
          <a href="%s" style="display:inline-block;padding:14px 32px;background:#38bdf8;color:#0a0f1a;font-weight:700;font-size:15px;border-radius:10px;text-decoration:none;">
            Sign in to FoxVue
          </a>
          <p style="margin:24px 0 0;font-size:13px;color:#64748b;line-height:1.6;">
            If you didn't create this account, you can safely ignore this email.
          </p>
        </td></tr>
        <tr><td style="padding:20px 40px;border-top:1px solid #1e2d3d;">
          <p style="margin:0;font-size:12px;color:#475569;text-align:center;">
            &copy; 2025 FoxVue. Do not reply to this email.
          </p>
        </td></tr>
      </table>
    </td></tr>
  </table>
</body>
</html>`, name, loginURL)
}

func buildVerificationEmail(name, code, verifyURL string) string {
	if name == "" {
		name = "there"
	}
	loginURL := os.Getenv("FRONTEND_URL")
	if loginURL == "" {
		loginURL = "http://localhost:5173"
	}
	loginURL += "/login"
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Verify your email</title>
</head>
<body style="margin:0;padding:0;background:#0a0f1a;font-family:system-ui,sans-serif;">
  <table width="100%%" cellpadding="0" cellspacing="0" style="background:#0a0f1a;padding:40px 20px;">
    <tr><td align="center">
      <table width="100%%" cellpadding="0" cellspacing="0" style="max-width:520px;background:#111827;border-radius:16px;border:1px solid #1e2d3d;overflow:hidden;">
        <tr><td style="height:4px;background:linear-gradient(90deg,#0ea5e9,#38bdf8,#7dd3fc);"></td></tr>
        <tr><td style="padding:36px 40px;">
          <h1 style="margin:0 0 4px;font-size:22px;font-weight:700;color:#f1f5f9;">FoxVue</h1>
          <p style="margin:0 0 28px;font-size:13px;color:#64748b;">AI-powered interview practice</p>
          <h2 style="margin:0 0 12px;font-size:18px;font-weight:600;color:#f1f5f9;">Verify your email</h2>
          <p style="margin:0 0 24px;font-size:15px;color:#94a3b8;line-height:1.6;">
            Hi %s, use the code below to verify your email and create your password. It expires in <strong style="color:#f1f5f9;">15 minutes</strong>.
          </p>
          <div style="text-align:center;margin:0 0 28px;">
            <span style="display:inline-block;padding:18px 40px;background:#0f172a;border:2px solid #38bdf8;border-radius:12px;font-size:36px;font-weight:800;letter-spacing:12px;color:#38bdf8;font-family:monospace;">%s</span>
          </div>
          <div style="text-align:center;margin:0 0 24px;">
            <a href="%s" style="display:inline-block;padding:14px 32px;background:#38bdf8;color:#0a0f1a;font-weight:700;font-size:15px;border-radius:10px;text-decoration:none;">
              Verify &amp; Create Password
            </a>
          </div>
          <p style="margin:0 0 16px;font-size:13px;color:#64748b;line-height:1.6;">
            Already verified? Head back to sign in.
          </p>
          <div style="text-align:center;margin:0 0 24px;">
            <a href="%s" style="display:inline-block;padding:10px 24px;background:transparent;color:#38bdf8;font-weight:600;font-size:14px;border-radius:10px;text-decoration:none;border:2px solid #38bdf8;">
              ← Back to Sign In
            </a>
          </div>
          <p style="margin:0;font-size:13px;color:#64748b;line-height:1.6;">
            If you didn't create a FoxVue account, you can safely ignore this email.
          </p>
        </td></tr>
        <tr><td style="padding:20px 40px;border-top:1px solid #1e2d3d;">
          <p style="margin:0;font-size:12px;color:#475569;text-align:center;">
            &copy; 2025 FoxVue. Do not reply to this email.
          </p>
        </td></tr>
      </table>
    </td></tr>
  </table>
</body>
</html>`, name, code, verifyURL, loginURL)
}
