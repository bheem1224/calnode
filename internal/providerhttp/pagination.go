package providerhttp

import "net/url"

// Never forward an OAuth bearer token to an origin supplied in a next-page URL.
func SameOrigin(base, next string) bool {
	a, err := url.Parse(base)
	if err != nil {
		return false
	}
	b, err := url.Parse(next)
	return err == nil && a.Scheme != "" && a.Host != "" && a.Scheme == b.Scheme && a.Host == b.Host && b.User == nil && b.Fragment == ""
}
