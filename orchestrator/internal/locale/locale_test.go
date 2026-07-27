package locale

import "testing"

func TestUnitedArabEmiratesRegionAndTimezone(t *testing.T) {
	if got := CountryName("AE"); got != "United Arab Emirates" {
		t.Fatalf("CountryName(AE) = %q", got)
	}
	if got := tzToCountry["Asia/Dubai"]; got != "AE" {
		t.Fatalf("Asia/Dubai maps to %q", got)
	}
	if got := DefaultTimezone("AE"); got != "Asia/Dubai" {
		t.Fatalf("DefaultTimezone(AE) = %q", got)
	}
	t.Setenv("TZ", "Asia/Dubai")
	t.Setenv("LC_ALL", "C")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LANG", "")
	t.Setenv("LANGUAGE", "")
	info := Detect()
	if info.Country != "AE" || info.CountryName != "United Arab Emirates" || info.Source != "timezone" {
		t.Fatalf("Dubai detection = %+v", info)
	}
}

func TestMultiTimezoneRegionsDoNotGuess(t *testing.T) {
	for _, code := range []string{"AU", "CA", "US"} {
		if got := DefaultTimezone(code); got != "" {
			t.Fatalf("DefaultTimezone(%s) should not guess, got %q", code, got)
		}
	}
}

func TestRegionsAreSortedAndUnique(t *testing.T) {
	regions := Regions()
	if len(regions) < 50 {
		t.Fatalf("expected a broad region catalog, got %d entries", len(regions))
	}
	seen := map[string]bool{}
	for i, region := range regions {
		if seen[region.Code] {
			t.Fatalf("duplicate region code %q", region.Code)
		}
		seen[region.Code] = true
		if i > 0 && regions[i-1].Name > region.Name {
			t.Fatalf("regions are not sorted: %q before %q", regions[i-1].Name, region.Name)
		}
	}
	if !seen["AE"] {
		t.Fatal("United Arab Emirates is missing from the region catalog")
	}
}
