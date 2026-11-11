package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

// Step is one choice of the ticket shop's wizard: "Category" and what was chosen, "Singles".
type Step struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Ticket is one ticket the shop lists once every step is chosen.
type Ticket struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Price     string `json:"price"`
}

// Page is what the shop shows after the given choices: the next step's options, or the tickets.
type Page struct {
	URL     string   `json:"url"`
	Title   string   `json:"title"`
	Dates   string   `json:"dates"`
	Steps   []Step   `json:"steps"`
	Options []string `json:"options"`
	Tickets []Ticket `json:"tickets"`
}

// Shop opens HYROX ticket shops in headless Chromium, as a visitor would. One page at a time:
// a Raspberry Pi has room for one Chromium, and the shop need not see more.
type Shop struct {
	Chromium string
	mu       sync.Mutex
}

// readPage reads the shop's state: the wizard is an <ol role=list> of steps with the current
// step's options as buttons right after it; each ticket has an "Add 1 … to the cart" button,
// disabled while it cannot be bought.
const readPage = `(() => {
  const text = (e) => (e?.innerText ?? '').trim();
  const ol = document.querySelector('ol[role=list]');
  const steps = ol ? [...ol.querySelectorAll('li > button')].map((b) => {
    const label = text(b.querySelector('span'));
    return { label, value: text(b).slice(label.length).trim() };
  }).filter((s) => !/^select/i.test(s.value)) : [];
  const tickets = [...document.querySelectorAll('button[aria-label^="Add 1 "][aria-label$=" to the cart"]')].map((b) => {
    const name = b.getAttribute('aria-label').replace(/^Add 1 /, '').replace(/ to the cart$/, '');
    const card = b.closest('[class*="group/card"]') ?? b.parentElement?.parentElement;
    // The price opens its breakdown; else the last amount on the card, after any in the description.
    const amounts = text(card.querySelector('[data-slot="dialog-trigger"]') ?? card).match(/[$€£]\s?[\d.,]+|[\d.,]+\s?[$€£]|CHF\s?[\d.,]+/g) ?? [''];
    const price = amounts[amounts.length - 1];
    return { name, available: !b.disabled, price };
  });
  const next = ol?.nextElementSibling;
  const options = tickets.length || !next ? [] : [...next.querySelectorAll(':scope > button')].map(text).filter(Boolean);
  const dates = [...document.querySelectorAll('time')].slice(0, 2).map(text).join(' ').trim();
  return { url: location.href, title: text(document.querySelector('h1')), dates, steps, options, tickets };
})()`

// clickText clicks the first visible button or link whose text is exactly the given one, ignoring
// case; whether there was one.
func clickText(text string) string {
	return fmt.Sprintf(`(() => {
  const want = %q.toLowerCase();
  const el = [...document.querySelectorAll('button, a')].find((e) => (e.innerText ?? '').trim().toLowerCase() === want && e.offsetParent !== null);
  if (el) el.click();
  return !!el;
})()`, text)
}

// Browse opens the event's shop, goes to its tickets and makes the given choices, one per step.
func (s *Shop) Browse(parent context.Context, event string, path []string) (*Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	flags := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(s.Chromium),
		chromedp.Flag("headless", "new"),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.WindowSize(1280, 2000),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(parent, flags...)
	defer allocCancel()
	ctx, cancel := chromedp.NewContext(allocCtx, chromedp.WithErrorf(quietLogf), chromedp.WithLogf(quietLogf))
	defer cancel()
	ctx, timeout := context.WithTimeout(ctx, 2*time.Minute)
	defer timeout()

	if err := chromedp.Run(ctx, chromedp.Navigate(english(event))); err != nil {
		return nil, fmt.Errorf("opening the shop: %w", err)
	}
	var ok bool
	// The cookie banner, if any, and then the way to the tickets.
	_ = chromedp.Run(ctx, chromedp.Sleep(2*time.Second), chromedp.Evaluate(clickText("Only necessary"), &ok))
	if err := poll(ctx, 20*time.Second, func() (bool, error) {
		return ok, chromedp.Run(ctx, chromedp.Evaluate(clickText("Buy Tickets"), &ok))
	}); err != nil {
		return nil, errors.New("no Buy Tickets button: is this a ticket shop's event page?")
	}
	page, err := settle(ctx, func(p *Page) bool { return len(p.Options) > 0 || len(p.Tickets) > 0 })
	if err != nil {
		return nil, errors.New("the shop showed neither choices nor tickets")
	}
	for _, choice := range path {
		if !contains(page.Options, choice) {
			return page, fmt.Errorf("the shop no longer offers %q", choice)
		}
		before := strings.Join(page.Options, "\n")
		if err := chromedp.Run(ctx, chromedp.Evaluate(clickText(choice), &ok)); err != nil || !ok {
			return page, fmt.Errorf("could not choose %q", choice)
		}
		// The next step's options, or the tickets.
		page, err = settle(ctx, func(p *Page) bool {
			return len(p.Tickets) > 0 || (len(p.Options) > 0 && strings.Join(p.Options, "\n") != before)
		})
		if err != nil {
			return nil, fmt.Errorf("nothing came after choosing %q", choice)
		}
	}
	return page, nil
}

// settle reads the page until done says it is there, or 20 s have passed.
func settle(ctx context.Context, done func(*Page) bool) (*Page, error) {
	var page Page
	err := poll(ctx, 20*time.Second, func() (bool, error) {
		page = Page{}
		if err := chromedp.Run(ctx, chromedp.Evaluate(readPage, &page)); err != nil {
			return false, err
		}
		return done(&page), nil
	})
	// A moment more: a list of tickets may still be filling in.
	if err == nil && len(page.Tickets) > 0 {
		time.Sleep(time.Second)
		_ = chromedp.Run(ctx, chromedp.Evaluate(readPage, &page))
	}
	return &page, err
}

func poll(ctx context.Context, limit time.Duration, try func() (bool, error)) error {
	deadline := time.Now().Add(limit)
	for {
		ok, err := try()
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return err
			}
			return errors.New("timed out")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// english asks for the shop in English, whose words the wizard's clicks look for.
func english(event string) string {
	u, err := url.Parse(event)
	if err != nil {
		return event
	}
	q := u.Query()
	q.Set("lang", "en")
	u.RawQuery = q.Encode()
	return u.String()
}

func contains(list []string, want string) bool {
	for _, each := range list {
		if each == want {
			return true
		}
	}
	return false
}

// quietLogf leaves out chromedp's chatter about events it does not handle.
func quietLogf(format string, args ...any) {
	if msg := fmt.Sprintf(format, args...); !strings.Contains(msg, "unhandled") {
		log.Print(msg)
	}
}

var shopLink = regexp.MustCompile(`https://[a-z0-9.-]+/event/[A-Za-z0-9_-]+`)

// ShopURL turns an event's page on hyrox.com into its ticket shop's, which that page links to as
// Athlete Tickets; a shop's own link stays as it is.
func ShopURL(ctx context.Context, link string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", errors.New("not a link: paste the event's page on hyrox.com or its ticket shop")
	}
	if host := strings.TrimPrefix(u.Hostname(), "www."); host != "hyrox.com" {
		u.RawQuery, u.Fragment = "", ""
		return u.String(), nil
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux aarch64) hyrox-radar")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("opening the event page: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	for _, found := range shopLink.FindAllString(string(body), -1) {
		if h, _ := url.Parse(found); h != nil && strings.TrimPrefix(h.Hostname(), "www.") != "hyrox.com" {
			return found, nil
		}
	}
	return "", errors.New("the event page links to no ticket shop yet")
}
