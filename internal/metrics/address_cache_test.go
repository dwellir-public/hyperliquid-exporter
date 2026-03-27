package metrics

import "testing"

func clearCache(t *testing.T) {
	t.Helper()
	ClearAddressCache()
}

func TestIsAddressTruncated(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"0x1234..5678", true},
		{"0x123456..abcd", true},
		{"0xAbCdEf0123456789AbCdEf0123456789AbCdEf01", false}, // full
		{"garbage", false},
		{"", false},
		{"0x12..34", false},   // too few hex before ..
		{"0x1234.5678", false}, // single dot
	}
	for _, tt := range tests {
		if got := IsAddressTruncated(tt.addr); got != tt.want {
			t.Errorf("IsAddressTruncated(%q) = %v, want %v", tt.addr, got, tt.want)
		}
	}
}

func TestRegisterAndExpand(t *testing.T) {
	clearCache(t)
	full := "0xAbCdEf0123456789AbCdEf0123456789AbCdEf01"
	RegisterFullAddress(full)

	// truncated form: "0xabcd..ef01" (lowercased)
	truncated := "0xabcd..ef01"
	got := ExpandAddress(truncated)
	want := "0xabcdef0123456789abcdef0123456789abcdef01"
	if got != want {
		t.Errorf("ExpandAddress(%q) = %q, want %q", truncated, got, want)
	}
}

func TestExpandUnknown(t *testing.T) {
	clearCache(t)
	addr := "0xdead..beef"
	got := ExpandAddress(addr)
	if got != addr {
		t.Errorf("expected input unchanged, got %q", got)
	}
}

func TestExpandFull(t *testing.T) {
	clearCache(t)
	full := "0xAbCdEf0123456789AbCdEf0123456789AbCdEf01"
	got := ExpandAddress(full)
	// should return lowercased but not truncated
	if got != "0xabcdef0123456789abcdef0123456789abcdef01" {
		t.Errorf("got %q, want lowercased input", got)
	}
}

func TestExpandEmpty(t *testing.T) {
	if got := ExpandAddress(""); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestRegisterShort(t *testing.T) {
	clearCache(t)
	RegisterFullAddress("0x1234") // < 42 chars
	if GetAddressCacheSize() != 0 {
		t.Error("short address should be ignored")
	}
}

func TestClearCacheWorks(t *testing.T) {
	clearCache(t)
	RegisterFullAddress("0xAbCdEf0123456789AbCdEf0123456789AbCdEf01")
	if GetAddressCacheSize() == 0 {
		t.Fatal("setup: expected entry after register")
	}
	ClearAddressCache()
	if GetAddressCacheSize() != 0 {
		t.Error("cache should be empty after clear")
	}
}

func TestCaseInsensitive(t *testing.T) {
	clearCache(t)
	RegisterFullAddress("0xAABBCC0123456789AABBCC0123456789AABBCC01")

	// expand with lowercase
	got := ExpandAddress("0xaabb..cc01")
	if got != "0xaabbcc0123456789aabbcc0123456789aabbcc01" {
		t.Errorf("got %q, want full lowercased address", got)
	}
}
