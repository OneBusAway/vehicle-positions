package rider

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// ShownFeedURL is a feed's configured address as the Rider mode page, the
// status endpoint and the log show it: without its user name, password, query
// and fragment, any of which can be the agency's credential for the feed. A
// viewer reads the first two, an admin need not be whoever set the server up,
// and a log is often kept where the server's settings are not.
func ShownFeedURL(raw string) string {
	u, err := url.Parse(raw)
	// With no host a mistyped address's user name is in its path.
	if err != nil || u.Host == "" {
		return "not a valid URL"
	}
	u.User, u.RawQuery, u.ForceQuery, u.Fragment = nil, "", false, ""
	return u.String()
}

// ShownGTFSSource is ShownFeedURL for GTFS_STATIC_URL, which FetchFeed reads
// as a local path unless it starts with http:// or https://. A path carries no
// credential and is shown as it is, but one with :// in it is a mistyped
// address.
func ShownGTFSSource(source string) string {
	if strings.Contains(source, "://") {
		return ShownFeedURL(source)
	}
	return source
}

// quoted is a string as %q writes it.
var quoted = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

// withoutURL is err without the address net/http and net/url put in front of
// the reason. Health and the log give the feed's address beside the error, and
// the one in it is whole, any key in its query included. An address that can't
// be parsed, the feed's own or one it redirects to, is quoted in the reason as
// well, so the reason is then only that it can't. Whatever else a reason quotes
// is left out of it: an answer that is not HTTP can repeat the request, and the
// scheme of an address without one is its user name.
func withoutURL(err error) error {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err
	}
	reason := urlErr.Err.Error()
	switch {
	case urlErr.Op == "parse":
		return errors.New("not a valid URL")
	case strings.HasPrefix(reason, "failed to parse Location header"):
		return errors.New("redirected to an address that is not a valid URL")
	case quoted.MatchString(reason):
		return errors.New(quoted.ReplaceAllString(reason, `"..."`))
	}
	return urlErr.Err
}
