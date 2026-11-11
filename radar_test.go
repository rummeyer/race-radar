package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeShop struct {
	page *Page
	err  error
}

func (f *fakeShop) Browse(context.Context, string, []string) (*Page, error) { return f.page, f.err }

type fakeEvents []Event

func (f fakeEvents) List(context.Context) ([]Event, error) { return f, nil }

func tickets(friday, saturday bool) *Page {
	return &Page{Title: "Sweatpals HYROX Miami Beach", TicketsURL: "https://shop/tickets/1", Tickets: []Ticket{
		{Name: "HYROX MEN | Friday", Available: friday, Price: "$218.33"},
		{Name: "HYROX MEN | Saturday", Available: saturday, Price: "$218.33"},
		{Name: "CHARITY: RACE FOR IMPACT | HYROX MEN | Friday", Available: true, Price: "$173.60"},
	}}
}

// radarWith is a radar over a fresh store with one watch for the Friday ticket, its messages kept.
func radarWith(t *testing.T, shop *fakeShop) (*Radar, *[]Message, string) {
	t.Helper()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	watch := &Watch{ID: "w1", Event: "Miami", URL: "https://shop/event/x", Path: []string{"Singles", "Open", "Men"}, Wanted: []string{"HYROX MEN | Friday"}}
	_ = store.Update(func(d *Data) error { d.Watches = append(d.Watches, watch); return nil })
	r := NewRadar(store, shop, fakeEvents{})
	var sent []Message
	r.send = func(_ context.Context, _ Settings, m Message) error { sent = append(sent, m); return nil }
	return r, &sent, watch.ID
}

func TestTellsOnceWhenAWantedTicketComesBack(t *testing.T) {
	shop := &fakeShop{page: tickets(false, false)}
	r, sent, id := radarWith(t, shop)
	ctx := context.Background()

	r.Check(ctx, id)
	if len(*sent) != 0 {
		t.Fatalf("sold out, yet told: %v", *sent)
	}
	shop.page = tickets(true, true)
	r.Check(ctx, id)
	if len(*sent) != 1 || !strings.Contains((*sent)[0].Text, "HYROX MEN | Friday $218.33") || strings.Contains((*sent)[0].Text, "Saturday") {
		t.Fatalf("want one alert for Friday only, got %+v", *sent)
	}
	if (*sent)[0].Priority != 1 || (*sent)[0].URL != "https://shop/tickets/1" {
		t.Errorf("alert should be high priority with the shop's tickets page: %+v", (*sent)[0])
	}
	r.Check(ctx, id)
	if len(*sent) != 1 {
		t.Fatalf("still available is no news: %+v", *sent)
	}
	shop.page = tickets(false, true)
	r.Check(ctx, id)
	shop.page = tickets(true, true)
	r.Check(ctx, id)
	if len(*sent) != 2 {
		t.Fatalf("gone and back again is news again: %+v", *sent)
	}
}

func TestTellsOfABrokenCheckOnceAndOfItsRecovery(t *testing.T) {
	shop := &fakeShop{err: errors.New("no Buy Tickets button")}
	r, sent, id := radarWith(t, shop)
	for range 5 {
		r.Check(context.Background(), id)
	}
	if len(*sent) != 1 || !strings.Contains((*sent)[0].Title, "broken") {
		t.Fatalf("want one broken alert after %d failures, got %+v", brokenAfter, *sent)
	}
	if w := r.store.Read().Find(id); w.Failures != 5 || w.Error == "" {
		t.Errorf("failures not kept: %+v", w)
	}
	shop.err, shop.page = nil, tickets(false, false)
	r.Check(context.Background(), id)
	if len(*sent) != 2 || !strings.Contains((*sent)[1].Title, "works again") {
		t.Fatalf("want a works-again alert, got %+v", *sent)
	}
	entries, _, stats := r.log.Snapshot()
	if stats.Checks != 6 || stats.Failures != 5 || stats.Alerts != 2 || entries[0].Kind != "alert" {
		t.Errorf("activity: %+v, newest %+v", stats, entries[0])
	}
}

func TestSearchTakesNoteFirstThenTellsOfNewRacesAndSales(t *testing.T) {
	berlin26 := Event{Name: "HYROX Berlin", URL: "https://hyrox.com/event/berlin-26/", Dates: "1. May. 2026", OnSale: true}
	s := &Search{Pattern: "Berlin"}
	if got := searched(s, []Event{berlin26}, time.Now()); len(got) != 0 {
		t.Fatalf("what is there when added is not news: %+v", got)
	}
	berlin27 := Event{Name: "HYROX Berlin", City: "BER", URL: "https://hyrox.com/event/berlin-27/", Dates: "1. May. 2027"}
	got := searched(s, []Event{berlin26, berlin27, {Name: "HYROX Hamburg", URL: "h"}}, time.Now())
	if len(got) != 1 || !strings.HasPrefix(got[0].Title, "New race") || !strings.Contains(got[0].Text, "not on sale yet") {
		t.Fatalf("want a new-race alert for 2027, got %+v", got)
	}
	berlin27.OnSale = true
	got = searched(s, []Event{berlin26, berlin27}, time.Now())
	if len(got) != 1 || !strings.HasPrefix(got[0].Title, "Tickets on sale") {
		t.Fatalf("want an on-sale alert, got %+v", got)
	}
	if len(s.Matches) != 2 {
		t.Errorf("matches: %+v", s.Matches)
	}
}

func TestMatches(t *testing.T) {
	berlin := Event{Name: "AIA HYROX Berlin", City: "BER"}
	for pattern, want := range map[string]bool{
		"Berlin": true, "berlin": true, "*Berlin*": true, "Berlin*": true, "ber": true, "AIA HYROX": true,
		"Hamburg": false, "": false, "*": false,
	} {
		if Matches(pattern, berlin) != want {
			t.Errorf("Matches(%q) = %v, want %v", pattern, !want, want)
		}
	}
}

const findMyRaceSnippet = `
<article class="w-grid-item post-57667 event type-event continent-north-america type-adults" data-id="57667"><div>
<div class="w-post-elm post_custom_field usg_post_custom_field_1 has_text_color type_text event_city_letter_code color_link_inherit"><span class="w-post-elm-value">MIA</span></div>
<h2 class="w-post-elm post_title"><a href="https://hyrox.com/event/sweat-pals-hyrox-miami-beach-26-27/">Sweatpals HYROX Miami Beach</a></h2>
<span class="w-post-elm post_custom_field usg_post_custom_field_2 type_text event_date_1 color_link_inherit"><span class="w-post-elm-value">26. Mar. 2027</span></span><span class="w-post-elm post_custom_field usg_post_custom_field_3 type_text event_date_3 color_link_inherit"><span class="w-post-elm-before">– </span><span class="w-post-elm-value">28. Mar. 2027</span></span>
<a class="w-btn" href="https://hyrox.com/event/sweat-pals-hyrox-miami-beach-26-27/"><span class="w-btn-label">Buy Tickets</span></a></div></article>
<article class="w-grid-item post-1 event type-event continent-africa type-adults" data-id="1"><div>
<div class="w-post-elm event_city_letter_code"><span class="w-post-elm-value">CPT</span></div>
<h2 class="w-post-elm post_title"><a href="https://hyrox.com/event/virgin-active-hyrox-cape-town/">Virgin Active HYROX Cape Town ||</a></h2>
<span class="w-post-elm event_date_1"><span class="w-post-elm-value">25. Mar. 2027</span></span>
<a class="w-btn" href="https://hyrox.com/event/virgin-active-hyrox-cape-town/"><span class="w-btn-label">Find out more</span></a></div></article>`

func TestParseEvents(t *testing.T) {
	events := ParseEvents(findMyRaceSnippet)
	if len(events) != 2 {
		t.Fatalf("want 2 events, got %+v", events)
	}
	cape, miami := events[0], events[1]
	if cape.Name != "Virgin Active HYROX Cape Town" || cape.OnSale || cape.Continent != "Africa" || cape.City != "CPT" {
		t.Errorf("Cape Town, soonest first: %+v", cape)
	}
	if miami.Dates != "26. Mar. 2027 – 28. Mar. 2027" || !miami.OnSale || miami.Continent != "North America" || miami.Start.Day() != 26 {
		t.Errorf("Miami: %+v", miami)
	}
}

func TestStoreKeepsItsFileToItsOwner(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(dir)
	_ = store.Update(func(d *Data) error { d.Settings.Pushover.Token = "secret"; return nil })
	info, err := os.Stat(filepath.Join(dir, "radar.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("radar.json should be 0600: %v %v", info.Mode(), err)
	}
	again, _ := OpenStore(dir)
	if again.Read().Settings.Pushover.Token != "secret" || again.Read().Settings.IntervalMinutes != 5 {
		t.Errorf("not kept: %+v", again.Read().Settings)
	}
}

func TestNotifyNeedsAChannelAndPushoverSendsToOneDevice(t *testing.T) {
	if err := Notify(context.Background(), Settings{}, Message{Title: "x"}); err == nil {
		t.Fatal("no channel on should be an error")
	}
	var form map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = map[string]string{"device": r.Form.Get("device"), "priority": r.Form.Get("priority"), "path": r.URL.Path}
		_, _ = w.Write([]byte(`{"status":1}`))
	}))
	defer server.Close()
	PushoverAPI = server.URL
	settings := Settings{Pushover: Pushover{Enabled: true, Token: "t", User: "u", Device: "Olivers-iPhone"}}
	if err := Notify(context.Background(), settings, Message{Title: "x", Priority: 1}); err != nil {
		t.Fatal(err)
	}
	if form["device"] != "Olivers-iPhone" || form["priority"] != "1" || form["path"] != "/messages.json" {
		t.Errorf("sent %+v", form)
	}
}
