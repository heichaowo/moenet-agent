package wireguard

import "testing"

// TestStripPort guards the endpoint parser used to geolocate NAT peers. A wrong
// split would report a garbled or empty IP to the CP, breaking CN enforcement
// (or worse, disabling the wrong peer). IPv6 in particular must not be truncated
// at its internal colons.
func TestStripPort(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"1.2.3.4:51820", "1.2.3.4"},
		{"203.0.113.9:1024", "203.0.113.9"},
		{"[2001:db8::1]:51820", "2001:db8::1"},
		{"[fe80::998:203:2:1]:443", "fe80::998:203:2:1"},
		// A bare IPv6 with no port/brackets must pass through untouched.
		{"2001:db8::1", "2001:db8::1"},
		// A bare IPv4 with no port passes through.
		{"1.2.3.4", "1.2.3.4"},
	}
	for _, tc := range cases {
		if got := stripPort(tc.in); got != tc.want {
			t.Errorf("stripPort(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
