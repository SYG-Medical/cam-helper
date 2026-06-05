package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed locales/*.json
var localeFiles embed.FS

var (
	currentLang  = "tr" // Default language
	translations = make(map[string]map[string]string)
)

func init() {
	// Load available languages from embedded files
	files, err := localeFiles.ReadDir("locales")
	if err != nil {
		fmt.Printf("Error reading locale files: %v\n", err)
		return
	}

	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		langCode := strings.TrimSuffix(file.Name(), ".json")
		
		data, err := localeFiles.ReadFile("locales/" + file.Name())
		if err != nil {
			fmt.Printf("Error reading locale %s: %v\n", file.Name(), err)
			continue
		}

		var langMap map[string]string
		if err := json.Unmarshal(data, &langMap); err != nil {
			fmt.Printf("Error parsing locale %s: %v\n", file.Name(), err)
			continue
		}

		translations[langCode] = langMap
	}
}

// Init sets the active language.
func Init(lang string) {
	if _, ok := translations[lang]; ok {
		currentLang = lang
	} else {
		// Fallback to English if "tr" is also somehow missing, but "tr" is our baseline
		currentLang = "tr"
	}
}

// T translates a given key based on the current language.
// If the key is not found, it returns the key itself as a fallback.
func T(key string, args ...interface{}) string {
	langMap, ok := translations[currentLang]
	if !ok {
		return formatStr(key, args...)
	}

	val, ok := langMap[key]
	if !ok {
		// Fallback to TR if key is missing in current language
		if trMap, ok := translations["tr"]; ok {
			if trVal, ok := trMap[key]; ok {
				return formatStr(trVal, args...)
			}
		}
		return formatStr(key, args...)
	}

	return formatStr(val, args...)
}

func formatStr(str string, args ...interface{}) string {
	if len(args) > 0 {
		return fmt.Sprintf(str, args...)
	}
	return str
}
