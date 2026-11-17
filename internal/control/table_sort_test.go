package control

import (
	"net/url"
	"testing"
	"time"
)

func TestMakeSortCol(t *testing.T) {
	// Inactive column with default asc
	q := url.Values{"q": []string{"dell"}}
	col1 := MakeSortCol("/installations", "used", "desc", "title", "Название", "asc", q)
	if col1.Active {
		t.Errorf("expected inactive col, got active")
	}
	if col1.Desc {
		t.Errorf("expected Desc=false for default asc")
	}
	if col1.Link != "/installations?dir=asc&q=dell&sort=title" {
		t.Errorf("unexpected link: %s", col1.Link)
	}

	// Active column with current dir asc -> next is desc
	col2 := MakeSortCol("/installations", "title", "asc", "title", "Название", "asc", q)
	if !col2.Active {
		t.Errorf("expected active col")
	}
	if col2.Desc {
		t.Errorf("expected current Desc=false")
	}
	if col2.Link != "/installations?dir=desc&q=dell&sort=title" {
		t.Errorf("unexpected link: %s", col2.Link)
	}

	// Active column with current dir desc -> next is asc
	col3 := MakeSortCol("/installations", "title", "desc", "title", "Название", "asc", q)
	if !col3.Active {
		t.Errorf("expected active col")
	}
	if !col3.Desc {
		t.Errorf("expected current Desc=true")
	}
	if col3.Link != "/installations?dir=asc&q=dell&sort=title" {
		t.Errorf("unexpected link: %s", col3.Link)
	}

	// Inactive column with default desc (e.g. used count)
	col4 := MakeSortCol("/installations", "title", "asc", "used", "Выбирали", "desc", q)
	if col4.Active {
		t.Errorf("expected inactive col")
	}
	if !col4.Desc {
		t.Errorf("expected default Desc=true")
	}
	if col4.Link != "/installations?dir=desc&q=dell&sort=used" {
		t.Errorf("unexpected link: %s", col4.Link)
	}
}

func TestCompareHelpers(t *testing.T) {
	// Strings
	if compareStrings("Alpha", "Beta", false) >= 0 {
		t.Errorf("expected Alpha < Beta")
	}
	if compareStrings("Alpha", "Beta", true) <= 0 {
		t.Errorf("expected Alpha > Beta when desc")
	}
	if compareStrings("", "Alpha", false) <= 0 {
		t.Errorf("expected empty string last")
	}
	if compareStrings("Beta", "", false) >= 0 {
		t.Errorf("expected non-empty before empty")
	}

	// Ints
	if compareInts(10, 20, false) >= 0 {
		t.Errorf("expected 10 < 20")
	}
	if compareInts(10, 20, true) <= 0 {
		t.Errorf("expected 10 > 20 when desc")
	}

	// Int64s
	if compareInt64s(100, 200, false) >= 0 {
		t.Errorf("expected 100 < 200")
	}
	if compareInt64s(100, 200, true) <= 0 {
		t.Errorf("expected 100 > 200 when desc")
	}

	// Times
	now := time.Now()
	later := now.Add(time.Hour)
	if compareTimes(now, later, false) >= 0 {
		t.Errorf("expected now < later")
	}
	if compareTimes(now, later, true) <= 0 {
		t.Errorf("expected now > later when desc")
	}
	if compareTimes(time.Time{}, now, false) <= 0 {
		t.Errorf("expected zero time last")
	}
}
