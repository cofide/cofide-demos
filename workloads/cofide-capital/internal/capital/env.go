package capital

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"strconv"
)

func Version() string {
	if v := os.Getenv("COFIDE_CAPITAL_VERSION"); v != "" {
		return v
	}
	return VersionV1
}

func IsV2() bool {
	return Version() == VersionV2
}

func Env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func EnvInt(name string, fallback int) int {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return i
}

func APIKey(name string) string {
	return Env(name, "cofide-capital-demo-secret")
}

func NewPaymentID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "PAY-demo"
	}
	return "PAY-" + hex.EncodeToString(b[:])
}
