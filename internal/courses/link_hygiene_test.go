package courses

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"testing"
)

func TestInspectLinkHTTPCanonicalization(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		canonical string
	}{
		{
			name:      "lowercases scheme and host",
			raw:       " HTTPS://EXAMPLE.TEST/Course?Token=A&token=b ",
			canonical: "https://example.test/Course?Token=A&token=b",
		},
		{
			name:      "removes default http port",
			raw:       "http://Example.Test:80/course",
			canonical: "http://example.test/course",
		},
		{
			name:      "removes default http port with leading zeroes",
			raw:       "http://Example.Test:080/course",
			canonical: "http://example.test/course",
		},
		{
			name:      "removes default https port",
			raw:       "https://Example.Test:443/course",
			canonical: "https://example.test/course",
		},
		{
			name:      "removes default https port with leading zeroes",
			raw:       "https://Example.Test:0443/course",
			canonical: "https://example.test/course",
		},
		{
			name:      "preserves signed query order and escaped path",
			raw:       "https://Example.Test/a%2Fb?sig=b&sig=a&x=1",
			canonical: "https://example.test/a%2Fb?sig=b&sig=a&x=1",
		},
		{
			name:      "drops fragment from identity",
			raw:       "https://Example.Test/course?x=1#section",
			canonical: "https://example.test/course?x=1",
		},
		{
			name:      "preserves ipv6 brackets",
			raw:       "https://[2001:db8::1]:443/course",
			canonical: "https://[2001:db8::1]/course",
		},
		{
			name:      "keeps nondefault port",
			raw:       "https://Example.Test:8443/course",
			canonical: "https://example.test:8443/course",
		},
		{
			name:      "keeps nondefault port without leading zero ambiguity",
			raw:       "https://Example.Test:08443/course",
			canonical: "https://example.test:8443/course",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, canonical, rejection := inspectLink(test.raw)
			if rejection != "" {
				t.Fatalf("rejection = %q, want accepted", rejection)
			}
			if parsed == nil {
				t.Fatal("parsed URL is nil")
			}
			if canonical != test.canonical {
				t.Fatalf("canonical = %q, want %q", canonical, test.canonical)
			}
		})
	}
}

func TestInspectLinkRejectsStructuralUnsafeLinks(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		rejection LinkRejection
	}{
		{name: "empty", raw: " \t\n ", rejection: "empty"},
		{name: "control", raw: "https://example.test/\x01course", rejection: "control_character"},
		{name: "unsupported scheme", raw: "javascript:alert(1)", rejection: "unsupported_scheme"},
		{name: "missing host", raw: "https:///missing-host", rejection: "missing_host"},
		{name: "credentials", raw: "https://user:pass@example.test/course", rejection: "credentials"},
		{name: "empty explicit port", raw: "https://example.test:/course", rejection: "invalid_port"},
		{name: "invalid port text", raw: "https://example.test:abc/course", rejection: "invalid_port"},
		{name: "invalid port range", raw: "https://example.test:65536/course", rejection: "invalid_port"},
		{name: "malformed escape", raw: "https://example.test/%zz", rejection: "invalid_url"},
		{name: "loopback literal", raw: "https://127.0.0.1/course", rejection: "local_target"},
		{name: "private literal", raw: "https://10.0.0.1/course", rejection: "local_target"},
		{name: "link local literal", raw: "https://169.254.1.1/course", rejection: "local_target"},
		{name: "unspecified literal", raw: "https://0.0.0.0/course", rejection: "local_target"},
		{name: "multicast literal", raw: "https://224.0.0.1/course", rejection: "local_target"},
		{name: "localhost", raw: "https://localhost/course", rejection: "local_target"},
		{name: "localhost suffix", raw: "https://course.localhost/course", rejection: "local_target"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, canonical, rejection := inspectLink(test.raw)
			if rejection != test.rejection {
				t.Fatalf("rejection = %q, want %q (parsed=%v canonical=%q)", rejection, test.rejection, parsed, canonical)
			}
			if canonical != "" {
				t.Fatalf("canonical = %q, want empty", canonical)
			}
		})
	}
}

func TestInspectLinkMagnetBTIHValidation(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		rejection LinkRejection
	}{
		{
			name: "valid hex btih",
			raw:  "magnet:?dn=Course&xt=urn:btih:0123456789abcdef0123456789ABCDEF01234567",
		},
		{
			name: "valid base32 btih",
			raw:  "magnet:?xt=urn:btih:ABCDEFGHIJKLMNOPQRSTUVWXYZ234567",
		},
		{
			name:      "missing btih",
			raw:       "magnet:?dn=Course",
			rejection: "invalid_magnet",
		},
		{
			name:      "short hex btih",
			raw:       "magnet:?xt=urn:btih:0123456789abcdef",
			rejection: "invalid_magnet",
		},
		{
			name:      "bad base32 btih",
			raw:       "magnet:?xt=urn:btih:ABCDEFGHIJKLMNOPQRSTUVWXY1234567",
			rejection: "invalid_magnet",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, canonical, rejection := inspectLink(test.raw)
			if rejection != test.rejection {
				t.Fatalf("rejection = %q, want %q", rejection, test.rejection)
			}
			if test.rejection == "" && canonical != test.raw {
				t.Fatalf("canonical = %q, want raw magnet identity", canonical)
			}
		})
	}
}

func TestLinkKeyAndHashUseCanonicalIdentity(t *testing.T) {
	key, err := linkKey(" HTTPS://EXAMPLE.TEST:443/a%2Fb?sig=b&sig=a#ignored ")
	if err != nil {
		t.Fatalf("linkKey: %v", err)
	}
	if key != "https://example.test/a%2Fb?sig=b&sig=a" {
		t.Fatalf("key = %q", key)
	}

	gotHash, err := linkHash("https://example.test/a%2Fb?sig=b&sig=a#other")
	if err != nil {
		t.Fatalf("linkHash: %v", err)
	}
	sum := sha256.Sum256([]byte(key))
	if want := hex.EncodeToString(sum[:]); gotHash != want {
		t.Fatalf("hash = %q, want %q", gotHash, want)
	}
	if _, err := linkKey("https://localhost/course"); err == nil {
		t.Fatal("linkKey accepted rejected local target")
	}
	if _, err := linkHash("javascript:alert(1)"); err == nil {
		t.Fatal("linkHash accepted rejected scheme")
	}
}

func FuzzInspectLink(f *testing.F) {
	for _, seed := range []string{
		"",
		"https://example.test/course",
		" HTTPS://EXAMPLE.TEST:443/course?b=2&a=1#x ",
		"https://127.0.0.1/course",
		"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
		"magnet:?dn=course",
		"https://example.test/%zz",
		"javascript:alert(1)",
		"https://example.test/\x00",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		parsed, canonical, rejection := inspectLink(raw)
		parsedAgain, canonicalAgain, rejectionAgain := inspectLink(raw)
		if (parsed == nil) != (parsedAgain == nil) || canonical != canonicalAgain || rejection != rejectionAgain {
			t.Fatalf("inspectLink is nondeterministic: (%v,%q,%q) then (%v,%q,%q)", parsed, canonical, rejection, parsedAgain, canonicalAgain, rejectionAgain)
		}
		if rejection != "" {
			return
		}
		if canonical == "" {
			t.Fatal("accepted link has empty canonical key")
		}
		reparsed, err := url.Parse(canonical)
		if err != nil {
			t.Fatalf("canonical does not reparse: %q: %v", canonical, err)
		}
		if parsed == nil || !strings.EqualFold(reparsed.Scheme, parsed.Scheme) {
			t.Fatalf("parsed/canonical scheme mismatch: parsed=%v canonical=%q", parsed, canonical)
		}
		firstHash, err := linkHash(raw)
		if err != nil {
			t.Fatalf("linkHash rejected accepted link: %v", err)
		}
		secondHash, err := linkHash(raw)
		if err != nil {
			t.Fatalf("linkHash second call rejected accepted link: %v", err)
		}
		if firstHash != secondHash || len(firstHash) != sha256.Size*2 {
			t.Fatalf("hash = %q then %q", firstHash, secondHash)
		}
	})
}
