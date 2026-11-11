package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// PushoverAPI is Pushover's API; tests point it elsewhere.
var PushoverAPI = "https://api.pushover.net/1"

// Message is one notification.
type Message struct {
	Title    string
	Text     string
	URL      string
	URLTitle string
	Priority int
}

// Send sends a notification to the configured user, or just their one device.
func (p Pushover) Send(ctx context.Context, m Message) error {
	if p.Token == "" || p.User == "" {
		return errors.New("Pushover is not set up: add the app token and user key in Settings")
	}
	form := url.Values{
		"token":    {p.Token},
		"user":     {p.User},
		"title":    {m.Title},
		"message":  {m.Text},
		"priority": {fmt.Sprint(m.Priority)},
	}
	if p.Device != "" {
		form.Set("device", p.Device)
	}
	if m.URL != "" {
		form.Set("url", m.URL)
		form.Set("url_title", m.URLTitle)
	}
	_, err := p.call(ctx, "/messages.json", form)
	return err
}

// Devices are the user's device names, for choosing one.
func (p Pushover) Devices(ctx context.Context) ([]string, error) {
	if p.Token == "" || p.User == "" {
		return nil, errors.New("add the app token and user key first")
	}
	answer, err := p.call(ctx, "/users/validate.json", url.Values{"token": {p.Token}, "user": {p.User}})
	if err != nil {
		return nil, err
	}
	return answer.Devices, nil
}

type pushoverAnswer struct {
	Status  int      `json:"status"`
	Errors  []string `json:"errors"`
	Devices []string `json:"devices"`
}

func (p Pushover) call(ctx context.Context, path string, form url.Values) (*pushoverAnswer, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, PushoverAPI+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Pushover: %w", err)
	}
	defer res.Body.Close()
	var answer pushoverAnswer
	_ = json.NewDecoder(res.Body).Decode(&answer)
	if answer.Status != 1 {
		if len(answer.Errors) > 0 {
			return nil, fmt.Errorf("Pushover: %s", strings.Join(answer.Errors, "; "))
		}
		return nil, fmt.Errorf("Pushover: HTTP %d", res.StatusCode)
	}
	return &answer, nil
}
