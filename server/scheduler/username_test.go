package scheduler

import (
	"errors"
	"testing"
	"time"
)

func TestParseUsername_OK(t *testing.T) {
	tok, err := ParseUsername("B_38313_US_5_Ab000001")
	if err != nil {
		t.Fatal(err)
	}
	if tok.UserID != "B_38313" || tok.Country != "US" ||
		tok.Duration != 5*time.Minute || tok.Session != "Ab000001" {
		t.Fatalf("bad tok: %+v", tok)
	}
	if tok.AuthKey() != "B_38313:Ab000001" {
		t.Fatal("auth key mismatch")
	}
}

func TestParseUsername_AnyCountry(t *testing.T) {
	for _, in := range []string{"B_38313__5_Ab000001", "B_38313_000_5_Ab000001"} {
		tok, err := ParseUsername(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if tok.Country != "" {
			t.Fatalf("%s: country should be unrestricted, got %q", in, tok.Country)
		}
	}
}

func TestParseUsername_DurationClamped(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"B_1_US_1_Ab000001", MinDuration},   // below floor
		{"B_1_US_5_Ab000001", 5 * time.Minute},
		{"B_1_US_90_Ab000001", MaxDuration},
		{"B_1_US_999_Ab000001", MaxDuration}, // above ceiling
	}
	for _, c := range cases {
		tok, err := ParseUsername(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if tok.Duration != c.want {
			t.Fatalf("%s: dur=%v, want %v", c.in, tok.Duration, c.want)
		}
	}
}

func TestParseUsername_UserIDWithUnderscores(t *testing.T) {
	// "B_38313" already has an underscore; ensure we don't lose it.
	tok, err := ParseUsername("B_38313_US_10_AbCdEf12")
	if err != nil {
		t.Fatal(err)
	}
	if tok.UserID != "B_38313" {
		t.Fatalf("user_id=%q", tok.UserID)
	}
}

func TestParseUsername_Bad(t *testing.T) {
	bad := []string{
		"",                       // empty
		"   ",                    // whitespace
		"B_38313_US_5",           // missing session
		"B_38313_USA_5_Ab000001", // 3-letter country
		"B_38313_US_x_Ab000001",  // non-numeric duration
		"B_38313_US_5_short",     // session too short
		"B_38313_US_5_Ab00000!",  // session non-alnum
		"_US_5_Ab000001",         // empty user_id
	}
	for _, s := range bad {
		if _, err := ParseUsername(s); err == nil {
			t.Errorf("%q: expected error", s)
		} else if !errors.Is(err, ErrBadUsername) {
			t.Errorf("%q: error not ErrBadUsername (got %v)", s, err)
		}
	}
}
