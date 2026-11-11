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
	"slices"
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
	URL string `json:"url"`
	// The shop's own page of the event's tickets, which opens at the first choice: a click
	// shorter than the event's page.
	TicketsURL string   `json:"ticketsUrl"`
	RacePage   string   `json:"racePage"`
	Title      string   `json:"title"`
	Dates      string   `json:"dates"`
	Steps      []Step   `json:"steps"`
	Options    []string `json:"options"`
	Tickets    []Ticket `json:"tickets"`
}

// Shop opens HYROX ticket shops in headless Chromium, as a visitor would. One page at a time:
// a Raspberry Pi has room for one Chromium, and the shop need not see more.
type Shop struct {
	Chromium string
	mu       sync.Mutex
}

// readPage reads the shop's state. vivenu, which runs HYROX's shops, has two designs. Both show
// the choices so far as an <ol role=list> of steps. The current one has the step's options as
// buttons right after it and an "Add 1 … to the cart" button per ticket, disabled while it
// cannot be bought. The classic checkout, as the DACH shop's, starts with the categories as
// links, has the later options as buttons after the steps, and a .ticket-type box per ticket,
// labelled Sold out while it is.
const readPage = `(() => {
  const text = (e) => (e?.innerText ?? '').trim();
  const line = (e) => text(e).split('\n')[0].trim();
  // A choice's name: its first line that is not a status badge, as some shops put above it.
  const status = /^(almost )?sold out$|^tickets? available$|^available$|^few (tickets )?left$|^new$|^coming soon$/i;
  const choice = (e) => text(e).split('\n').map((l) => l.trim()).find((l) => l && !status.test(l)) ?? '';
  const amounts = (e) => text(e).match(/[$€£¥₩]\s?[\d.,]+|[\d.,]+\s?(?:[$€£]|kr|zł|Kč|Ft|lei)|(?:CHF|AED|SGD|HKD|AUD|NZD|CAD|R\$|MX\$|A\$|NZ\$|S\$|HK\$)\s?[\d.,]+/g) ?? [''];
  const ol = document.querySelector('ol[role=list]');
  const steps = ol ? [...ol.querySelectorAll('li > button')].map((b) => {
    const label = text(b.querySelector('span'));
    return { label, value: text(b).slice(label.length).trim() };
  }).filter((s) => s.value && !/^select/i.test(s.value)) : [];
  // The classic checkout first: it has "Add 1 … to the cart" buttons too, but only on the tickets
  // that can be bought, so they would leave out the sold-out ones (11.11.2026: Osaka).
  let tickets = [...document.querySelectorAll('.ticket-type')].map((card) => {
    const buttons = [...card.querySelectorAll('.buttons button, .buttons select, .buttons input')];
    const soldOut = /sold out|unavailable|not available|ausverkauft|épuisé|agotad|esaurit/i.test(text(card.querySelector('.label')));
    return {
      name: text(card.querySelector('.info p') ?? card.querySelector('p')),
      available: !soldOut && buttons.some((b) => !b.disabled && !/^remove/i.test(b.getAttribute('aria-label') ?? '')),
      price: text(card.querySelector('.price')) || amounts(card)[0],
    };
  }).filter((t) => t.name);
  if (!tickets.length) {
    tickets = [...document.querySelectorAll('button[aria-label^="Add 1 "][aria-label$=" to the cart"]')].map((b) => {
      const name = b.getAttribute('aria-label').replace(/^Add 1 /, '').replace(/ to the cart$/, '');
      const card = b.closest('[class*="group/card"]') ?? b.parentElement?.parentElement;
      // The price opens its breakdown; else the last amount on the card, after any in the description.
      const found = amounts(card.querySelector('[data-slot="dialog-trigger"]') ?? card);
      return { name, available: !b.disabled, price: found[found.length - 1] };
    });
  }
  let options = [];
  if (!tickets.length) {
    const holder = ol?.nextElementSibling ?? document.querySelector('#current-step .categories, .categories');
    options = holder ? [...holder.querySelectorAll('a, button')].filter((e) => !e.closest('.ticket-type')).map(choice).filter(Boolean) : [];
  }
  // The classic checkout says "12/10/2026 until 12/13/2026" beneath the race's name.
  const until = [...document.querySelectorAll('div, span')].find((e) => !e.children.length && /^\d{1,2}[./]\d{1,2}[./]\d{4} until \d{1,2}[./]\d{1,2}[./]\d{4}$/i.test(text(e)));
  const dates = [...document.querySelectorAll('time')].slice(0, 2).map(text).join(' ').trim() || text(until).replace(/\s+until\s+/i, ' – ');
  const title = text(document.querySelector('h1')) || document.title.replace(/^Tickets for /i, '').replace(/\s*\|\s*vivenu$/i, '').trim();
  return { url: location.href, title, dates, steps, options, tickets };
})()`

// classicCheckout is the classic design's checkout page, which its Buy tickets opens in a window
// over the event's page: the event's shop settings name it. Empty in the current design.
const classicCheckout = `(() => {
  const m = document.documentElement.innerHTML.match(/"shop":\{[^{}]*?"eventId":"([0-9a-f]{24})"/);
  return m ? location.origin + '/checkout/' + m[1] : '';
})()`

// clickText clicks the first visible button or link whose text, or one of its lines, is the given
// one, ignoring case: a choice may have a status badge above its name. Whether there was one.
func clickText(text string) string {
	return fmt.Sprintf(`(() => {
  const want = %q.toLowerCase();
  const said = (e) => (e.innerText ?? '').trim().toLowerCase();
  const el = [...document.querySelectorAll('button, a')].find((e) => e.offsetParent !== null && (said(e) === want || said(e).split('\n').some((l) => l.trim() === want)));
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
	var checkout string
	_ = chromedp.Run(ctx, chromedp.Sleep(2*time.Second), chromedp.Evaluate(classicCheckout, &checkout))
	if checkout != "" {
		if err := chromedp.Run(ctx, chromedp.Navigate(english(checkout)), chromedp.Sleep(2*time.Second)); err != nil {
			return nil, fmt.Errorf("opening the checkout: %w", err)
		}
	}
	// The cookie banner, if any, and then the way to the tickets: the classic checkout is there.
	_ = chromedp.Run(ctx, chromedp.Evaluate(clickText("Only necessary"), &ok))
	if checkout == "" {
		if err := poll(ctx, 20*time.Second, func() (bool, error) {
			return ok, chromedp.Run(ctx, chromedp.Evaluate(clickText("Buy Tickets"), &ok))
		}); err != nil {
			return nil, errors.New("no Buy Tickets button: is this a ticket shop's event page?")
		}
	}
	page, err := settle(ctx, func(p *Page) bool { return len(p.Options) > 0 || len(p.Tickets) > 0 })
	if err != nil {
		return nil, errors.New("the shop showed neither choices nor tickets")
	}
	for _, choice := range path {
		if !slices.Contains(page.Options, choice) {
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
	if strings.Contains(page.URL, "/tickets/") || strings.Contains(page.URL, "/checkout/") {
		page.TicketsURL = strings.Split(page.URL, "?")[0]
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

// poll tries until try says it is done, every half second, for at most limit: try's last error,
// or a timeout.
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

// quietLogf leaves out chromedp's chatter about events it does not handle.
func quietLogf(format string, args ...any) {
	if msg := fmt.Sprintf(format, args...); !strings.Contains(msg, "unhandled") {
		log.Print(msg)
	}
}

var shopLink = regexp.MustCompile(`https://[a-z0-9.-]+/event/[A-Za-z0-9_-]+`)

// ErrNoShop is a race whose page links to no ticket shop yet.
var ErrNoShop = errors.New("the race page links to no ticket shop yet")

var regionShop = regexp.MustCompile(`^[a-z0-9-]+\.hyrox\.[a-z.]+$`)

// bestShop picks the race's ticket shop among the event links on its page. A race page may also
// link a local site's Youngstars race first (Singapore, Hong Kong): a region's shop
// (singapore.hyrox.com) and a season's event (…-season-26-27-…) come first, Youngstars last
// unless the race is one.
func bestShop(links []string, youngstars bool) string {
	best, bestScore := "", -1
	for _, link := range links {
		u, err := url.Parse(link)
		if err != nil || !isShop(u.Hostname()) {
			continue
		}
		score := 0
		if regionShop.MatchString(u.Hostname()) {
			score += 4
		}
		if strings.Contains(u.Path, "season") {
			score += 2
		}
		if strings.Contains(u.Path, "youngstars") != youngstars {
			score -= 8
		}
		if score > bestScore {
			best, bestScore = link, score
		}
	}
	return best
}

// isShop says whether an event link on a race's page is to its ticket shop, not to hyrox.com
// itself, its hotels (eventpipe) or its volunteers (rosterfy).
func isShop(host string) bool {
	host = strings.TrimPrefix(host, "www.")
	return host != "hyrox.com" && !strings.Contains(host, "eventpipe") && !strings.Contains(host, "rosterfy")
}

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
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux aarch64) race-radar")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("opening the event page: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if best := bestShop(shopLink.FindAllString(string(body), -1), strings.Contains(u.Path, "youngstars")); best != "" {
		return best, nil
	}
	return "", ErrNoShop
}
