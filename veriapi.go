// Package veriapi, VeriApi.Pro için resmî Go istemcisidir (yalnızca standart kitaplık).
package veriapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Surum, SDK sürümüdür.
const Surum = "0.1.0"

const (
	varsayilanAdres   = "https://api.veriapi.pro"
	varsayilanBekleme = time.Second // 429'da Retry-After yoksa
)

// Kaynak, yanıttaki veri kaynağı ve atıf bilgisidir.
type Kaynak struct {
	Kod    string  `json:"kod"`
	Ad     string  `json:"ad"`
	Lisans *string `json:"lisans"`
	Atif   *string `json:"atif"`
	URL    *string `json:"url"`
}

// Kota, X-RateLimit-* ve X-Istek-Maliyeti başlıklarıdır; başlık yoksa alan nil.
type Kota struct {
	GunlukLimit    *int
	GunlukKalan    *int
	DakikalikLimit *int
	DakikalikKalan *int
	IstekMaliyeti  *int
}

// Sonuc, çözülmüş yanıt zarfıdır. Zarfsız uçlarda (/v1/durum) Veri gövdenin tamamıdır.
type Sonuc struct {
	Veri       json.RawMessage
	Kaynak     []Kaynak
	Guncelleme *string
	Bayat      bool
	Kota       Kota
}

// Coz, Veri alanını hedef değişkene çözer.
func (s *Sonuc) Coz(hedef any) error {
	return json.Unmarshal(s.Veri, hedef)
}

// Alan, 422 yanıtındaki hatalı parametredir.
type Alan struct {
	Alan  string `json:"alan"`
	Mesaj string `json:"mesaj"`
}

// Hata, API ya da bağlantı hatasıdır. Bağlantı ve zaman aşımında Kod ve HTTPDurumu 0'dır.
type Hata struct {
	Kod        int
	Mesaj      string
	HTTPDurumu int
	// Alanlar, 422'de hatalı parametrelerdir.
	Alanlar []Alan
	// RetryAfter, 429'da beklenecek süredir; başlık yoksa 0.
	RetryAfter time.Duration
	Kota       Kota
	ZamanAsimi bool
	// Neden, alttaki hatadır (bağlantı hatalarında).
	Neden error
}

func (h *Hata) Error() string { return h.Mesaj }

// Unwrap, alttaki hatayı döndürür.
func (h *Hata) Unwrap() error { return h.Neden }

// Client, VeriApi.Pro istemcisidir; eşzamanlı kullanıma uygundur.
type Client struct {
	anahtar       string
	temelAdres    string
	zamanAsimi    time.Duration
	yenidenDeneme int
	azamiBekleme  time.Duration
	http          *http.Client
	uyu           func(context.Context, time.Duration) error
}

// Option, istemci seçeneğidir.
type Option func(*Client)

// WithTemelAdres, temel adresi değiştirir (varsayılan https://api.veriapi.pro).
func WithTemelAdres(adres string) Option {
	return func(c *Client) { c.temelAdres = strings.TrimRight(adres, "/") }
}

// WithZamanAsimi, istek başına süreyi belirler (varsayılan 10 sn).
func WithZamanAsimi(sure time.Duration) Option {
	return func(c *Client) { c.zamanAsimi = sure }
}

// WithYenidenDeneme, 429'da en çok kaç kez daha deneneceğini belirler (varsayılan 2, 0 kapatır).
func WithYenidenDeneme(adet int) Option {
	return func(c *Client) {
		if adet < 0 {
			adet = 0
		}
		c.yenidenDeneme = adet
	}
}

// WithAzamiBekleme, en uzun Retry-After bekleyişini belirler (varsayılan 60 sn).
// Bundan uzun süre beklenmez, hata hemen döner; günlük kota aşımı saatler sürebilir.
func WithAzamiBekleme(sure time.Duration) Option {
	return func(c *Client) { c.azamiBekleme = sure }
}

// WithHTTPClient, özel http.Client verir. Kopyası kullanılır, yönlendirmeler yine izlenmez.
func WithHTTPClient(istemci *http.Client) Option {
	return func(c *Client) {
		if istemci != nil {
			kopya := *istemci
			kopya.CheckRedirect = yonlendirmeYok
			c.http = &kopya
		}
	}
}

// X-API-Key başka bir adrese taşınmasın: yönlendirme izlenmez, 3xx hata olur.
func yonlendirmeYok(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func uyu(ctx context.Context, sure time.Duration) error {
	sayac := time.NewTimer(sure)
	defer sayac.Stop()
	select {
	case <-sayac.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// New, istemci oluşturur.
func New(anahtar string, secenekler ...Option) *Client {
	c := &Client{
		anahtar:       anahtar,
		temelAdres:    varsayilanAdres,
		zamanAsimi:    10 * time.Second,
		yenidenDeneme: 2,
		azamiBekleme:  60 * time.Second,
		http:          &http.Client{CheckRedirect: yonlendirmeYok},
		uyu:           uyu,
	}
	for _, secenek := range secenekler {
		secenek(c)
	}
	return c
}

type hamYanit struct {
	durum     int
	govde     []byte
	basliklar http.Header
}

// Get, GET isteği atar. yol "/v1/doviz" ya da "doviz" olabilir; boş değerli parametreler gönderilmez.
// Hatalar *Hata tipindedir (errors.As).
func (c *Client) Get(ctx context.Context, yol string, parametreler map[string]string) (*Sonuc, error) {
	if c.anahtar == "" {
		return nil, &Hata{Mesaj: "API anahtarı gerekli"}
	}
	adres := c.url(yol, parametreler)
	for deneme := 0; ; deneme++ {
		yanit, err := c.istek(ctx, adres)
		if err != nil {
			return nil, err
		}
		if yanit.durum == http.StatusTooManyRequests && deneme < c.yenidenDeneme {
			bekle, bulundu := retryAfter(yanit.basliklar.Get("Retry-After"))
			if !bulundu {
				bekle = varsayilanBekleme
			}
			if bekle <= c.azamiBekleme {
				if err := c.uyu(ctx, bekle); err != nil {
					return nil, &Hata{Mesaj: "Bekleme iptal edildi: " + err.Error(), Neden: err}
				}
				continue
			}
		}
		if yanit.durum < 200 || yanit.durum >= 300 {
			return nil, hataOlustur(yanit)
		}
		return coz(yanit)
	}
}

func (c *Client) url(yol string, parametreler map[string]string) string {
	if !strings.HasPrefix(yol, "/v1/") {
		yol = "/v1/" + strings.TrimLeft(yol, "/")
	}
	sorgu := url.Values{}
	for ad, deger := range parametreler {
		if deger != "" {
			sorgu.Set(ad, deger)
		}
	}
	if len(sorgu) == 0 {
		return c.temelAdres + yol
	}
	ayrac := "?"
	if strings.Contains(yol, "?") {
		ayrac = "&"
	}
	return c.temelAdres + yol + ayrac + sorgu.Encode()
}

func (c *Client) istek(ctx context.Context, adres string) (*hamYanit, error) {
	istekCtx, iptal := context.WithTimeout(ctx, c.zamanAsimi)
	defer iptal()
	istek, err := http.NewRequestWithContext(istekCtx, http.MethodGet, adres, nil)
	if err != nil {
		return nil, &Hata{Mesaj: "Geçersiz adres: " + err.Error(), Neden: err}
	}
	istek.Header.Set("X-API-Key", c.anahtar)
	istek.Header.Set("Accept", "application/json")
	istek.Header.Set("User-Agent", "veriapi-go/"+Surum)
	yanit, err := c.http.Do(istek)
	if err != nil {
		return nil, c.baglantiHatasi(err)
	}
	defer yanit.Body.Close()
	govde, err := io.ReadAll(yanit.Body)
	if err != nil {
		return nil, c.baglantiHatasi(err)
	}
	return &hamYanit{durum: yanit.StatusCode, govde: govde, basliklar: yanit.Header}, nil
}

func (c *Client) baglantiHatasi(err error) *Hata {
	if errors.Is(err, context.DeadlineExceeded) {
		return &Hata{Mesaj: fmt.Sprintf("Zaman aşımı (%s)", c.zamanAsimi), ZamanAsimi: true, Neden: err}
	}
	return &Hata{Mesaj: "Bağlantı hatası: " + err.Error(), Neden: err}
}

func tam(deger string) *int {
	sayi, err := strconv.Atoi(strings.TrimSpace(deger))
	if err != nil {
		return nil
	}
	return &sayi
}

func kotaOku(b http.Header) Kota {
	return Kota{
		GunlukLimit:    tam(b.Get("X-RateLimit-Limit-Day")),
		GunlukKalan:    tam(b.Get("X-RateLimit-Remaining-Day")),
		DakikalikLimit: tam(b.Get("X-RateLimit-Limit-Minute")),
		DakikalikKalan: tam(b.Get("X-RateLimit-Remaining-Minute")),
		IstekMaliyeti:  tam(b.Get("X-Istek-Maliyeti")),
	}
}

func retryAfter(deger string) (time.Duration, bool) {
	saniye, err := strconv.ParseFloat(strings.TrimSpace(deger), 64)
	if err != nil || math.IsNaN(saniye) {
		return 0, false
	}
	if saniye < 0 {
		saniye = 0
	}
	if saniye > 1e9 { // time.Duration taşmasın
		saniye = 1e9
	}
	return time.Duration(saniye * float64(time.Second)), true
}

func hataOlustur(yanit *hamYanit) *Hata {
	hata := &Hata{
		Kod:        yanit.durum,
		Mesaj:      fmt.Sprintf("Beklenmeyen yanıt (HTTP %d)", yanit.durum),
		HTTPDurumu: yanit.durum,
		Kota:       kotaOku(yanit.basliklar),
	}
	hata.RetryAfter, _ = retryAfter(yanit.basliklar.Get("Retry-After"))
	var belge struct {
		Hata *struct {
			Kod   *int            `json:"kod"`
			Mesaj json.RawMessage `json:"mesaj"`
		} `json:"hata"`
	}
	// JSON olmayan yanıtta (vekil sunucu 502 sayfası vb.) genel mesaj kalır
	if err := json.Unmarshal(yanit.govde, &belge); err != nil || belge.Hata == nil {
		return hata
	}
	if belge.Hata.Kod != nil {
		hata.Kod = *belge.Hata.Kod
	}
	var metin string
	if err := json.Unmarshal(belge.Hata.Mesaj, &metin); err == nil {
		if metin != "" {
			hata.Mesaj = metin
		}
		return hata
	}
	// 422: {"metin": "...", "alanlar": [...]}
	var ayrinti struct {
		Metin   string `json:"metin"`
		Alanlar []Alan `json:"alanlar"`
	}
	if err := json.Unmarshal(belge.Hata.Mesaj, &ayrinti); err == nil {
		if ayrinti.Metin != "" {
			hata.Mesaj = ayrinti.Metin
		}
		hata.Alanlar = ayrinti.Alanlar
	}
	return hata
}

func coz(yanit *hamYanit) (*Sonuc, error) {
	kota := kotaOku(yanit.basliklar)
	if !json.Valid(yanit.govde) {
		return nil, &Hata{Mesaj: "Yanıt JSON değil", HTTPDurumu: yanit.durum, Kota: kota}
	}
	sonuc := &Sonuc{Veri: json.RawMessage(yanit.govde), Kaynak: []Kaynak{}, Kota: kota}
	var alanlar map[string]json.RawMessage
	if err := json.Unmarshal(yanit.govde, &alanlar); err != nil {
		return sonuc, nil
	}
	veri, zarfli := alanlar["veri"]
	if !zarfli {
		return sonuc, nil
	}
	var zarf struct {
		Kaynak     []Kaynak `json:"kaynak"`
		Guncelleme *string  `json:"guncelleme"`
		Bayat      bool     `json:"bayat"`
	}
	if err := json.Unmarshal(yanit.govde, &zarf); err != nil {
		return nil, &Hata{Mesaj: "Yanıt zarfı çözülemedi: " + err.Error(), HTTPDurumu: yanit.durum, Kota: kota, Neden: err}
	}
	sonuc.Veri = veri
	if zarf.Kaynak != nil {
		sonuc.Kaynak = zarf.Kaynak
	}
	sonuc.Guncelleme = zarf.Guncelleme
	sonuc.Bayat = zarf.Bayat
	return sonuc, nil
}

// Sayi, Enlem gibi isteğe bağlı sayı alanları için işaretçi üretir (0 geçerli değer olduğundan).
func Sayi(deger float64) *float64 { return &deger }

func ondalik(deger *float64) string {
	if deger == nil {
		return ""
	}
	return strconv.FormatFloat(*deger, 'f', -1, 64)
}

func tamMetin(deger int) string {
	if deger == 0 {
		return ""
	}
	return strconv.Itoa(deger)
}

// DovizParametreleri: Kod ör. []string{"USD", "EUR"}; Tur "anlik" ya da "referans".
type DovizParametreleri struct {
	Kod []string
	Tur string
}

// KonumParametreleri: Konum slug'ı, Il (ad, slug ya da plaka) + Ilce, ya da Enlem + Boylam.
type KonumParametreleri struct {
	Konum  string
	Il     string
	Ilce   string
	Enlem  *float64
	Boylam *float64
}

func (p KonumParametreleri) harita() map[string]string {
	return map[string]string{
		"konum":  p.Konum,
		"il":     p.Il,
		"ilce":   p.Ilce,
		"enlem":  ondalik(p.Enlem),
		"boylam": ondalik(p.Boylam),
	}
}

// NamazParametreleri: Tarih YYYY-AA-GG (varsayılan bugün), Gun 1-31.
type NamazParametreleri struct {
	KonumParametreleri
	Tarih string
	Gun   int
}

// DepremParametreleri, /v1/deprem/son süzgeçleridir.
type DepremParametreleri struct {
	Limit       int
	MinBuyukluk *float64
	Saat        int
	Il          string
	Yurtici     bool
}

// Doviz, /v1/doviz ucunu çağırır.
func (c *Client) Doviz(ctx context.Context, p DovizParametreleri) (*Sonuc, error) {
	return c.Get(ctx, "/v1/doviz", map[string]string{"kod": strings.Join(p.Kod, ","), "tur": p.Tur})
}

// Hava, /v1/hava ucunu çağırır.
func (c *Client) Hava(ctx context.Context, p KonumParametreleri) (*Sonuc, error) {
	return c.Get(ctx, "/v1/hava", p.harita())
}

// Namaz, /v1/namaz ucunu çağırır.
func (c *Client) Namaz(ctx context.Context, p NamazParametreleri) (*Sonuc, error) {
	parametreler := p.harita()
	parametreler["tarih"] = p.Tarih
	parametreler["gun"] = tamMetin(p.Gun)
	return c.Get(ctx, "/v1/namaz", parametreler)
}

// Depremler, /v1/deprem/son ucunu çağırır.
func (c *Client) Depremler(ctx context.Context, p DepremParametreleri) (*Sonuc, error) {
	parametreler := map[string]string{
		"limit":        tamMetin(p.Limit),
		"min_buyukluk": ondalik(p.MinBuyukluk),
		"saat":         tamMetin(p.Saat),
		"il":           p.Il,
	}
	if p.Yurtici {
		parametreler["yurtici"] = "true"
	}
	return c.Get(ctx, "/v1/deprem/son", parametreler)
}

// Iller, /v1/konumlar/iller ucunu çağırır.
func (c *Client) Iller(ctx context.Context) (*Sonuc, error) {
	return c.Get(ctx, "/v1/konumlar/iller", nil)
}

// Ilceler, ilin ilçelerini getirir; il ad, slug ya da plaka olabilir.
func (c *Client) Ilceler(ctx context.Context, il string) (*Sonuc, error) {
	return c.Get(ctx, "/v1/konumlar/iller/"+url.PathEscape(il)+"/ilceler", nil)
}
