// Package locale reports the machine's timezone and infers its country/region
// WITHOUT any network or IP lookup — it reads only the OS timezone and locale.
// This keeps the privacy promise (nothing leaves the machine) while still letting
// CloudlessOS tailor itself, e.g. recommend Mistral models on a machine in France.
package locale

import (
	"fmt"
	"os"
	"sort"
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

// timezone resolves the IANA zone name from $TZ, the active /etc/localtime
// symlink, or the legacy /etc/timezone file. The symlink is authoritative:
// timedatectl can update it while /etc/timezone remains stale on Ubuntu.
func timezone() string {
	if tz := strings.TrimSpace(os.Getenv("TZ")); tz != "" {
		return strings.TrimPrefix(tz, ":")
	}
	if p, err := os.Readlink("/etc/localtime"); err == nil {
		if i := strings.LastIndex(p, "zoneinfo/"); i >= 0 {
			return p[i+len("zoneinfo/"):]
		}
	}
	if b, err := os.ReadFile("/etc/timezone"); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
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

// Region is a country/region offered by the profile picker. The catalog lives
// here, beside CountryName, so API responses and the UI cannot drift apart.
type Region struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Timezone string `json:"timezone,omitempty"`
}

// Regions returns the supported profile regions, alphabetized for display.
func Regions() []Region {
	regions := make([]Region, 0, len(countryNames))
	for code, name := range countryNames {
		regions = append(regions, Region{Code: code, Name: name, Timezone: DefaultTimezone(code)})
	}
	sort.Slice(regions, func(i, j int) bool { return regions[i].Name < regions[j].Name })
	return regions
}

// DefaultTimezone returns a safe country-wide default when a region has one.
// Countries with several meaningful timezones are intentionally omitted so a
// broad region choice never silently moves the user's clock to the wrong coast.
func DefaultTimezone(code string) string {
	return countryDefaultTimezones[strings.ToUpper(strings.TrimSpace(code))]
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
	"Europe/Bucharest":  "RO",
	"Europe/Budapest":   "HU",
	"Europe/Sofia":      "BG",
	"Europe/Zagreb":     "HR",
	"Europe/Belgrade":   "RS",
	"Europe/Kyiv":       "UA",
	"Europe/Istanbul":   "TR",
	// Middle East / Africa
	"Asia/Dubai":          "AE",
	"Asia/Riyadh":         "SA",
	"Asia/Qatar":          "QA",
	"Asia/Kuwait":         "KW",
	"Asia/Bahrain":        "BH",
	"Asia/Muscat":         "OM",
	"Asia/Jerusalem":      "IL",
	"Africa/Cairo":        "EG",
	"Africa/Casablanca":   "MA",
	"Africa/Johannesburg": "ZA",
	"Africa/Lagos":        "NG",
	"Africa/Nairobi":      "KE",
	// the Americas / APAC commonly seen in dev
	"America/New_York":               "US",
	"America/Chicago":                "US",
	"America/Denver":                 "US",
	"America/Los_Angeles":            "US",
	"America/Toronto":                "CA",
	"America/Vancouver":              "CA",
	"America/Mexico_City":            "MX",
	"America/Sao_Paulo":              "BR",
	"America/Argentina/Buenos_Aires": "AR",
	"Asia/Tokyo":                     "JP",
	"Asia/Shanghai":                  "CN",
	"Asia/Hong_Kong":                 "HK",
	"Asia/Seoul":                     "KR",
	"Asia/Singapore":                 "SG",
	"Asia/Kolkata":                   "IN",
	"Asia/Jakarta":                   "ID",
	"Asia/Kuala_Lumpur":              "MY",
	"Asia/Manila":                    "PH",
	"Asia/Bangkok":                   "TH",
	"Asia/Ho_Chi_Minh":               "VN",
	"Asia/Taipei":                    "TW",
	"Australia/Sydney":               "AU",
	"Pacific/Auckland":               "NZ",
}

var countryNames = map[string]string{
	"AE": "United Arab Emirates", "AR": "Argentina", "AT": "Austria",
	"AU": "Australia", "BE": "Belgium", "BG": "Bulgaria", "BH": "Bahrain",
	"BR": "Brazil", "CA": "Canada", "CH": "Switzerland", "CL": "Chile",
	"CN": "China", "CO": "Colombia", "CR": "Costa Rica", "HR": "Croatia",
	"CY": "Cyprus", "CZ": "Czechia", "DE": "Germany", "DK": "Denmark",
	"EE": "Estonia", "EG": "Egypt", "ES": "Spain", "FI": "Finland",
	"FR": "France", "GB": "United Kingdom", "GR": "Greece", "HK": "Hong Kong",
	"HU": "Hungary", "ID": "Indonesia", "IE": "Ireland", "IL": "Israel",
	"IN": "India", "IS": "Iceland", "IT": "Italy", "JP": "Japan",
	"KE": "Kenya", "KR": "South Korea", "KW": "Kuwait", "LT": "Lithuania",
	"LU": "Luxembourg", "LV": "Latvia", "MA": "Morocco", "MX": "Mexico",
	"MY": "Malaysia", "NG": "Nigeria", "NL": "Netherlands", "NO": "Norway",
	"NZ": "New Zealand", "OM": "Oman", "PE": "Peru", "PH": "Philippines",
	"PL": "Poland", "PT": "Portugal", "QA": "Qatar", "RO": "Romania",
	"RS": "Serbia", "SA": "Saudi Arabia", "SE": "Sweden", "SG": "Singapore",
	"SK": "Slovakia", "SI": "Slovenia", "TH": "Thailand", "TR": "Türkiye",
	"TW": "Taiwan", "UA": "Ukraine", "US": "United States", "VN": "Vietnam",
	"ZA": "South Africa",
}

var countryDefaultTimezones = map[string]string{
	"AE": "Asia/Dubai", "AT": "Europe/Vienna", "BE": "Europe/Brussels",
	"BG": "Europe/Sofia", "BH": "Asia/Bahrain", "CH": "Europe/Zurich",
	"CN": "Asia/Shanghai", "CO": "America/Bogota", "CR": "America/Costa_Rica",
	"CY": "Asia/Nicosia", "CZ": "Europe/Prague", "DE": "Europe/Berlin",
	"DK": "Europe/Copenhagen", "EE": "Europe/Tallinn", "EG": "Africa/Cairo",
	"FI": "Europe/Helsinki", "GB": "Europe/London", "GR": "Europe/Athens",
	"HK": "Asia/Hong_Kong", "HR": "Europe/Zagreb", "HU": "Europe/Budapest",
	"IE": "Europe/Dublin", "IL": "Asia/Jerusalem", "IN": "Asia/Kolkata",
	"IS": "Atlantic/Reykjavik", "IT": "Europe/Rome", "JP": "Asia/Tokyo",
	"KE": "Africa/Nairobi", "KR": "Asia/Seoul", "KW": "Asia/Kuwait",
	"LT": "Europe/Vilnius", "LU": "Europe/Luxembourg", "LV": "Europe/Riga",
	"MA": "Africa/Casablanca", "MY": "Asia/Kuala_Lumpur", "NG": "Africa/Lagos",
	"NL": "Europe/Amsterdam", "NO": "Europe/Oslo", "OM": "Asia/Muscat",
	"PE": "America/Lima", "PH": "Asia/Manila", "PL": "Europe/Warsaw",
	"QA": "Asia/Qatar", "RO": "Europe/Bucharest", "RS": "Europe/Belgrade",
	"SA": "Asia/Riyadh", "SE": "Europe/Stockholm", "SG": "Asia/Singapore",
	"SI": "Europe/Ljubljana", "SK": "Europe/Bratislava", "TH": "Asia/Bangkok",
	"TR": "Europe/Istanbul", "TW": "Asia/Taipei", "UA": "Europe/Kyiv",
	"VN": "Asia/Ho_Chi_Minh", "ZA": "Africa/Johannesburg",
}
