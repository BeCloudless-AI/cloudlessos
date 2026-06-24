// Package locale reports the machine's timezone and infers its country/region
// WITHOUT any network or IP lookup — it reads only the OS timezone and locale.
// This keeps the privacy promise (nothing leaves the machine) while still letting
// CloudlessOS tailor itself, e.g. recommend Mistral models on a machine in France.
package locale

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Info is the detected locale/timezone picture for the machine.
type Info struct {
	Timezone      string `json:"timezone"`      // IANA zone, e.g. "Europe/Paris" ("" = unknown)
	UTCOffset     string `json:"utcOffset"`     // current offset, e.g. "+02:00"
	Locale        string `json:"locale"`        // raw locale env, e.g. "fr_FR.UTF-8" ("" = unset)
	LocaleCountry string `json:"localeCountry"` // ISO-3166 alpha-2 from locale, e.g. "FR"
	TZCountry     string `json:"tzCountry"`     // ISO-3166 alpha-2 from timezone, e.g. "FR"
	Country       string `json:"country"`       // best-effort detected country (locale, else timezone)
	CountryName   string `json:"countryName"`   // human name, e.g. "France"
	Source        string `json:"source"`        // how Country was inferred: "locale" | "timezone" | ""
}

// Detect reads the OS timezone + locale and infers a country. Pure local I/O.
func Detect() Info {
	tz := timezone()
	in := Info{
		Timezone:  tz,
		UTCOffset: offset(),
		Locale:    localeEnv(),
	}
	in.LocaleCountry = countryFromLocale(in.Locale)
	in.TZCountry = tzToCountry[tz]

	switch {
	case in.LocaleCountry != "":
		in.Country, in.Source = in.LocaleCountry, "locale"
	case in.TZCountry != "":
		in.Country, in.Source = in.TZCountry, "timezone"
	}
	in.CountryName = CountryName(in.Country)
	return in
}

// timezone resolves the IANA zone name from $TZ, /etc/timezone, or the
// /etc/localtime symlink (the three places Linux distros keep it).
func timezone() string {
	if tz := strings.TrimSpace(os.Getenv("TZ")); tz != "" {
		return strings.TrimPrefix(tz, ":")
	}
	if b, err := os.ReadFile("/etc/timezone"); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}
	if p, err := os.Readlink("/etc/localtime"); err == nil {
		if i := strings.LastIndex(p, "zoneinfo/"); i >= 0 {
			return p[i+len("zoneinfo/"):]
		}
	}
	return ""
}

// offset formats the machine's current UTC offset as ±HH:MM.
func offset() string {
	_, secs := time.Now().Zone()
	sign := "+"
	if secs < 0 {
		sign, secs = "-", -secs
	}
	return fmt.Sprintf("%s%02d:%02d", sign, secs/3600, (secs%3600)/60)
}

// localeEnv returns the first set locale env var (LC_ALL > LC_CTYPE > LANG > LANGUAGE).
func localeEnv() string {
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG", "LANGUAGE"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" && v != "C" && v != "POSIX" {
			return v
		}
	}
	return ""
}

// countryFromLocale pulls the country from "ll_CC.encoding" / "ll-CC" forms.
func countryFromLocale(loc string) string {
	if loc == "" {
		return ""
	}
	s := loc
	if i := strings.IndexAny(s, ".@"); i >= 0 {
		s = s[:i]
	}
	s = strings.ReplaceAll(s, "-", "_")
	parts := strings.Split(s, "_")
	if len(parts) >= 2 && len(parts[1]) == 2 {
		return strings.ToUpper(parts[1])
	}
	return ""
}

// CountryName maps an ISO-3166 alpha-2 code to a display name (best-effort; falls
// back to the code itself for codes not in the table).
func CountryName(code string) string {
	if code == "" {
		return ""
	}
	if n, ok := countryNames[strings.ToUpper(code)]; ok {
		return n
	}
	return strings.ToUpper(code)
}

// tzToCountry maps the IANA zones we care about to ISO country codes. France is
// covered thoroughly (metropolitan + overseas); common Western zones are included
// so the display is useful elsewhere too. Unknown zones leave Country empty.
var tzToCountry = map[string]string{
	// France — metropolitan and overseas territories
	"Europe/Paris":       "FR",
	"Indian/Reunion":     "FR",
	"Indian/Mayotte":     "FR",
	"Indian/Kerguelen":   "FR",
	"America/Martinique": "FR",
	"America/Guadeloupe": "FR",
	"America/Cayenne":    "FR",
	"America/Miquelon":   "FR",
	"Pacific/Noumea":     "FR",
	"Pacific/Tahiti":     "FR",
	"Pacific/Gambier":    "FR",
	"Pacific/Marquesas":  "FR",
	"Pacific/Wallis":     "FR",
	// neighbours / common Europe
	"Europe/Brussels":   "BE",
	"Europe/Amsterdam":  "NL",
	"Europe/Luxembourg": "LU",
	"Europe/Madrid":     "ES",
	"Europe/Lisbon":     "PT",
	"Europe/Berlin":     "DE",
	"Europe/Zurich":     "CH",
	"Europe/Rome":       "IT",
	"Europe/London":     "GB",
	"Europe/Dublin":     "IE",
	"Europe/Vienna":     "AT",
	"Europe/Stockholm":  "SE",
	"Europe/Oslo":       "NO",
	"Europe/Copenhagen": "DK",
	"Europe/Warsaw":     "PL",
	"Europe/Prague":     "CZ",
	"Europe/Athens":     "GR",
	"Europe/Helsinki":   "FI",
	// the Americas / APAC commonly seen in dev
	"America/New_York":    "US",
	"America/Chicago":     "US",
	"America/Denver":      "US",
	"America/Los_Angeles": "US",
	"America/Toronto":     "CA",
	"America/Sao_Paulo":   "BR",
	"Asia/Tokyo":          "JP",
	"Asia/Shanghai":       "CN",
	"Asia/Singapore":      "SG",
	"Asia/Kolkata":        "IN",
	"Australia/Sydney":    "AU",
}

var countryNames = map[string]string{
	"FR": "France", "BE": "Belgium", "NL": "Netherlands", "LU": "Luxembourg",
	"ES": "Spain", "PT": "Portugal", "DE": "Germany", "CH": "Switzerland",
	"IT": "Italy", "GB": "United Kingdom", "IE": "Ireland", "AT": "Austria",
	"SE": "Sweden", "NO": "Norway", "DK": "Denmark", "PL": "Poland",
	"CZ": "Czechia", "GR": "Greece", "FI": "Finland", "US": "United States",
	"CA": "Canada", "BR": "Brazil", "JP": "Japan", "CN": "China",
	"SG": "Singapore", "IN": "India", "AU": "Australia",
}
