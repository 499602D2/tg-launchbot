package db

// TODO FUTURE save in a database table + cache (V3.1)
// - when a new LSP ID is encountered in /launch/upcoming endpoint, request its info and insert into LSP table
// Currently contains all featured launch providers + a couple extra
// https://ll.thespacedevs.com/2.2.0/agencies/?featured=true&limit=50
var LSPShorthands = map[int]LSP{
	// Agencies
	17: {Name: "CNSA", Flag: "🇨🇳", Cc: "CHN"},
	31: {Name: "ISRO", Flag: "🇮🇳", Cc: "IND"},
	37: {Name: "JAXA", Flag: "🇯🇵", Cc: "JPN"},
	44: {Name: "NASA", Flag: "🇺🇸", Cc: "USA"},
	63: {Name: "ROSCOSMOS", Flag: "🇷🇺", Cc: "RUS"},

	// Corporations, including state and commercial
	88:  {Name: "CASC", Flag: "🇨🇳", Cc: "CHN"},
	96:  {Name: "KhSC", Flag: "🇷🇺", Cc: "RUS"},
	98:  {Name: "Mitsubishi H.I.", Flag: "🇯🇵", Cc: "JPN"},
	115: {Name: "Arianespace", Flag: "🇪🇺", Cc: "EU"},
	121: {Name: "SpaceX", Flag: "🇺🇸", Cc: "USA"},
	124: {Name: "ULA", Flag: "🇺🇸", Cc: "USA"},
	141: {Name: "Blue Origin", Flag: "🇺🇸", Cc: "USA"},
	147: {Name: "Rocket Lab", Flag: "🇺🇸", Cc: "USA"},
	190: {Name: "Antrix Corp.", Flag: "🇮🇳", Cc: "IND"},
	193: {Name: "RUS Space Forces", Flag: "🇷🇺", Cc: "RUS"},
	194: {Name: "ExPace", Flag: "🇨🇳", Cc: "CHN"},
	199: {Name: "Virgin Orbit", Flag: "🇺🇸", Cc: "USA"},
	257: {Name: "Northrop Grumman", Flag: "🇺🇸", Cc: "USA"},
	259: {Name: "LandSpace", Flag: "🇨🇳", Cc: "CHN"},
	265: {Name: "Firefly", Flag: "🇺🇸", Cc: "USA"},
	266: {Name: "Relativity", Flag: "🇺🇸", Cc: "USA"},
	274: {Name: "iSpace", Flag: "🇨🇳", Cc: "CHN"},
	285: {Name: "Astra", Flag: "🇺🇸", Cc: "USA"},

	// Small-scale providers, incl. sub-orbital operators
	1002: {Name: "Interstellar tech.", Flag: "🇯🇵", Cc: "JPN"},
	1021: {Name: "Galactic Energy", Flag: "🇨🇳", Cc: "CHN"},
	1024: {Name: "Virgin Galactic", Flag: "🇺🇸", Cc: "USA"},
	1029: {Name: "TiSPACE", Flag: "🇹🇼", Cc: "TWN"},
	1030: {Name: "ABL", Flag: "🇺🇸", Cc: "USA"},
	1038: {Name: "ELA", Flag: "🇦🇺", Cc: "AUS"},
}

// Map country codes to a list of provider IDs under this country code
var IdByCountryCode = map[string][]int{
	"USA": {44, 121, 124, 141, 147, 199, 257, 265, 266, 285, 1024, 1030},
	"EU":  {115},
	"CHN": {17, 88, 194, 259, 274, 1021},
	"RUS": {63, 96},
	"IND": {31, 190},
	"JPN": {37, 98, 1002},
	"TWN": {1029},
	"AUS": {1038},
}

// List of available countries (EU is effectively a faux-country)
var CountryCodes = []string{"USA", "EU", "CHN", "RUS", "IND", "JPN", "TWN", "AUS"}

var CountryCodeToName = map[string]string{
	"USA": "USA 🇺🇸", "EU": "EU 🇪🇺", "CHN": "China 🇨🇳", "RUS": "Russia 🇷🇺",
	"IND": "India 🇮🇳", "JPN": "Japan 🇯🇵", "TWN": "Taiwan 🇹🇼", "AUS": "Australia 🇦🇺",
}

type LSP struct {
	Name string
	Flag string
	Cc   string
}

// Extend the LaunchProvider type to get a short name, if one exists
func (provider *LaunchProvider) ShortName() string {
	// Check if a short name exists
	_, ok := LSPShorthands[provider.Id]

	if ok {
		return LSPShorthands[provider.Id].Name
	}

	// Log long names we encounter
	if len(provider.Name) > len("Virgin Orbit") {
		// TODO only warn once (keep track of warned LSP IDs)
		// log.Warn().Msgf("Provider name '%s' not found in LSPShorthands, id=%d (not warning again)",
		// 	provider.Name, provider.Id)

		return provider.Abbrev
	}

	return provider.Name
}
