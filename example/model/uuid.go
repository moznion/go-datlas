package model

import (
	"encoding/hex"
	"fmt"
)

// UnmarshalText parses the canonical form xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx in either case.
// Implementing encoding.TextUnmarshaler lets master and seed data use UUID values (see masterdata).
func (u *UUID) UnmarshalText(text []byte) error {
	if len(text) != 36 || text[8] != '-' || text[13] != '-' || text[18] != '-' || text[23] != '-' {
		return fmt.Errorf("invalid UUID %q", text)
	}
	var digits []byte
	for i, c := range text {
		if i != 8 && i != 13 && i != 18 && i != 23 {
			digits = append(digits, c)
		}
	}
	if _, err := hex.Decode(u[:], digits); err != nil {
		return fmt.Errorf("invalid UUID %q: %w", text, err)
	}
	return nil
}

// String returns the canonical lower-case form.
func (u UUID) String() string {
	s := hex.EncodeToString(u[:])
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

// MarshalText returns the canonical form. Implementing encoding.TextMarshaler lets the generated
// ApplyMasterData and ApplySeedData write UUID values to the database as text.
func (u UUID) MarshalText() ([]byte, error) { return []byte(u.String()), nil }
