package providerlimits

import "strings"

// NormalizeProfileLabel converts a raw plan/subscription identifier (e.g. an
// Anthropic subscriptionType or an OpenAI plan_type) into the "profile-<slug>"
// AccountLabel shape the sanitize layer accepts. Returns "" when the input has
// no usable characters.
func NormalizeProfileLabel(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	var builder strings.Builder
	previousDash := false
	for _, character := range raw {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9', character == '_', character == '-':
			builder.WriteRune(character)
			previousDash = false
		default:
			if !previousDash {
				builder.WriteByte('-')
				previousDash = true
			}
		}
	}
	normalized := strings.Trim(builder.String(), "-_")
	if normalized == "" {
		return ""
	}
	if len(normalized) > 48 {
		normalized = normalized[:48]
	}
	return "profile-" + normalized
}
