package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Notify sends a message every way that is switched on; an error if none is, or one failed.
func Notify(ctx context.Context, s Settings, m Message) error {
	var errs []error
	sent := false
	if s.Pushover.Enabled {
		sent = true
		errs = append(errs, s.Pushover.Send(ctx, m))
	}
	if s.Email.Enabled {
		sent = true
		errs = append(errs, s.Email.Send(m))
	}
	if !sent {
		return errors.New("no notification switched on: set up Pushover or email in Settings")
	}
	return errors.Join(errs...)
}

// Send mails the message.
func (e Email) Send(m Message) error {
	if err := e.send(m); err != nil {
		return fmt.Errorf("email: %w", err)
	}
	return nil
}

// send is Send, its errors not yet saying they are the email's.
func (e Email) send(m Message) error {
	if e.Host == "" || e.To == "" {
		return errors.New("add the SMTP server and the address to send to")
	}
	port := e.Port
	if port == 0 {
		port = 587
	}
	from := e.From
	if from == "" {
		from = e.Username
	}
	to := splitAddresses(e.To)
	body := m.Text
	if m.URL != "" {
		body += "\n\n" + m.URLTitle + ": " + m.URL
	}
	msg := strings.Join([]string{
		"From: Race Radar <" + from + ">",
		"To: " + strings.Join(to, ", "),
		"Subject: " + mime.QEncoding.Encode("utf-8", m.Title),
		"Date: " + time.Now().Format(time.RFC1123Z),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: 8bit",
		"",
		body,
	}, "\r\n")

	addr := net.JoinHostPort(e.Host, fmt.Sprint(port))
	var auth smtp.Auth
	if e.Username != "" {
		auth = smtp.PlainAuth("", e.Username, e.Password, e.Host)
	}
	if port != 465 {
		// STARTTLS where the server offers it, which smtp.SendMail does by itself.
		return smtp.SendMail(addr, auth, from, to, []byte(msg))
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 20 * time.Second}, "tcp", addr, &tls.Config{ServerName: e.Host})
	if err != nil {
		return err
	}
	client, err := smtp.NewClient(conn, e.Host)
	if err != nil {
		return err
	}
	defer client.Close()
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// splitAddresses reads "a@example.com, b@example.com": separated by commas, semicolons or spaces.
func splitAddresses(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' })
}
