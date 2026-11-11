package main

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// FindMyRace is HYROX's list of every event.
const FindMyRace = "https://hyrox.com/find-my-race/"

// Event is one race from Find my race.
type Event struct {
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	City      string    `json:"city"`
	Continent string    `json:"continent"`
	Start     time.Time `json:"start"`
	Dates     string    `json:"dates"`
	Youth     bool      `json:"youth"`
	OnSale    bool      `json:"onSale"`
}

var (
	articleRe   = regexp.MustCompile(`(?s)<article class="([^"]*\bevent\b[^"]*)"[^>]*>(.*?)</article>`)
	titleRe     = regexp.MustCompile(`(?s)<h2[^>]*>\s*<a href="([^"]+)">(.*?)</a>`)
	cityRe      = regexp.MustCompile(`event_city_letter_code[^>]*><span class="w-post-elm-value">([^<]*)<`)
	date1Re     = regexp.MustCompile(`event_date_1[^>]*><span class="w-post-elm-value">([^<]*)<`)
	date3Re     = regexp.MustCompile(`event_date_3[^>]*>(?:<span class="w-post-elm-before">[^<]*</span>)?<span class="w-post-elm-value">([^<]*)<`)
	continentRe = regexp.MustCompile(`\bcontinent-([a-z-]+)`)
)

// ParseEvents reads the events off Find my race's page, soonest first.
func ParseEvents(page string) []Event {
	var events []Event
	for _, m := range articleRe.FindAllStringSubmatch(page, -1) {
		class, body := m[1], m[2]
		t := titleRe.FindStringSubmatch(body)
		if t == nil {
			continue
		}
		e := Event{
			URL:    t[1],
			Name:   strings.TrimSpace(strings.TrimRight(html.UnescapeString(t[2]), "| ")),
			Youth:  strings.Contains(class, "type-youngstars"),
			OnSale: strings.Contains(body, ">Buy Tickets<"),
		}
		if c := cityRe.FindStringSubmatch(body); c != nil {
			e.City = strings.TrimSpace(c[1])
		}
		if c := continentRe.FindStringSubmatch(class); c != nil {
			e.Continent = strings.Title(strings.ReplaceAll(c[1], "-", " "))
		}
		from, to := "", ""
		if d := date1Re.FindStringSubmatch(body); d != nil {
			from = strings.TrimSpace(d[1])
		}
		if d := date3Re.FindStringSubmatch(body); d != nil {
			to = strings.TrimSpace(d[1])
		}
		e.Start = parseDate(from)
		e.Dates = from
		if to != "" && to != from {
			e.Dates = from + " – " + to
		}
		events = append(events, e)
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Start.Before(events[j].Start) })
	return events
}

// parseDate reads "26. Mar. 2027"; zero if it cannot.
func parseDate(s string) time.Time {
	s = strings.NewReplacer(".", "", "Sept", "Sep", "June", "Jun", "July", "Jul").Replace(s)
	for _, layout := range []string{"2 Jan 2006", "2 January 2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// Matches says whether a race's name or city code contains the text looked for, case aside.
// Asterisks are left out: "*Berlin*" is "Berlin".
func Matches(text string, e Event) bool {
	text = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(text, "*", "")))
	if text == "" {
		return false
	}
	return strings.Contains(strings.ToLower(e.Name), text) || strings.Contains(strings.ToLower(e.City), text)
}

// Events keeps Find my race's list for half an hour: it changes with new events, not by the minute.
type Events struct {
	mu      sync.Mutex
	fetched time.Time
	list    []Event
}

func (e *Events) List(ctx context.Context) ([]Event, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.list != nil && time.Since(e.fetched) < 30*time.Minute {
		return e.list, nil
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, FindMyRace, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux aarch64) hyrox-radar")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return e.list, fmt.Errorf("Find my race: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return e.list, fmt.Errorf("Find my race: HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return e.list, err
	}
	list := ParseEvents(string(body))
	if len(list) == 0 {
		return e.list, fmt.Errorf("Find my race: no events found, the page may have changed")
	}
	e.list, e.fetched = list, time.Now()
	return list, nil
}
