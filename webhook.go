package veriapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// VarsayilanTolerans, yeniden oynatmaya karşı önerilen zaman farkı sınırıdır.
const VarsayilanTolerans = 5 * time.Minute

var (
	imzaDeseni = regexp.MustCompile(`^sha256=([0-9a-fA-F]{64})$`)
	sayiDeseni = regexp.MustCompile(`^\d+(\.\d+)?$`)
)

// ImzaDogrula, X-VeriApi-Imza başlığını doğrular; geçersiz her girdide false döner.
// İmzalanan metin `<zaman>.<ham gövde>`, HMAC-SHA256 hex; karşılaştırma hmac.Equal ile sabit zamanlıdır.
// govde ayrıştırılmamış ham gövdedir; imza ve zaman X-VeriApi-Imza ve X-VeriApi-Zaman başlıklarıdır.
// tolerans 0 ise zaman farkı denetlenmez.
func ImzaDogrula(gizli string, govde []byte, imza, zaman string, tolerans time.Duration) bool {
	return imzaDogrulaAn(gizli, govde, imza, zaman, tolerans, time.Now())
}

func imzaDogrulaAn(gizli string, govde []byte, imza, zaman string, tolerans time.Duration, simdi time.Time) bool {
	zaman = strings.TrimSpace(zaman)
	if gizli == "" || zaman == "" {
		return false
	}
	eslesme := imzaDeseni.FindStringSubmatch(strings.TrimSpace(imza))
	if eslesme == nil {
		return false
	}
	gelen, err := hex.DecodeString(eslesme[1])
	if err != nil {
		return false
	}
	ozet := hmac.New(sha256.New, []byte(gizli))
	ozet.Write([]byte(zaman + "."))
	ozet.Write(govde)
	if !hmac.Equal(ozet.Sum(nil), gelen) {
		return false
	}
	if tolerans <= 0 {
		return true
	}
	saniye, gecerli := zamanOku(zaman)
	if !gecerli {
		return false
	}
	fark := math.Abs(float64(simdi.UnixNano())/1e9 - saniye)
	return fark <= tolerans.Seconds()
}

// zamanOku: Unix saniyesi ya da saat dilimli ISO 8601 (RFC 3339).
func zamanOku(zaman string) (float64, bool) {
	if sayiDeseni.MatchString(zaman) {
		saniye, err := strconv.ParseFloat(zaman, 64)
		return saniye, err == nil
	}
	an, err := time.Parse(time.RFC3339, zaman)
	if err != nil {
		return 0, false
	}
	return float64(an.UnixNano()) / 1e9, true
}
