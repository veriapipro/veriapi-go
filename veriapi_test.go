package veriapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const zarfMetni = `{"veri":{"kurlar":[{"kod":"USD"}]},"kaynak":[{"kod":"ecb","ad":"ECB","lisans":"x","atif":"y","url":"z"}],"guncelleme":"2026-11-05T10:00:00","bayat":true}`

var kotaBasliklari = map[string]string{
	"X-RateLimit-Limit-Day":        "1000",
	"X-RateLimit-Remaining-Day":    "999",
	"X-RateLimit-Limit-Minute":     "20",
	"X-RateLimit-Remaining-Minute": "19",
	"X-Istek-Maliyeti":             "1",
}

type sahteYanit struct {
	durum     int
	basliklar map[string]string
	govde     string
	gecikme   time.Duration
}

type sahteSunucu struct {
	*httptest.Server
	mu       sync.Mutex
	yanitlar []sahteYanit
	istekler []*http.Request
}

func sahteKur(t *testing.T, yanitlar ...sahteYanit) *sahteSunucu {
	t.Helper()
	s := &sahteSunucu{yanitlar: yanitlar}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.istekler = append(s.istekler, r)
		yanit := sahteYanit{durum: 200, basliklar: kotaBasliklari, govde: zarfMetni}
		if len(s.yanitlar) > 0 {
			yanit = s.yanitlar[0]
			s.yanitlar = s.yanitlar[1:]
		}
		s.mu.Unlock()
		if yanit.gecikme > 0 {
			select {
			case <-time.After(yanit.gecikme):
			case <-r.Context().Done():
				return
			}
		}
		for ad, deger := range yanit.basliklar {
			w.Header().Set(ad, deger)
		}
		w.WriteHeader(yanit.durum)
		_, _ = w.Write([]byte(yanit.govde))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *sahteSunucu) son() *http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.istekler[len(s.istekler)-1]
}

func (s *sahteSunucu) adet() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.istekler)
}

// istemci, beklemeleri kaydeden (gerçekten beklemeyen) istemci kurar.
func (s *sahteSunucu) istemci(beklemeler *[]time.Duration, secenekler ...Option) *Client {
	c := New("va_test", append([]Option{WithTemelAdres(s.URL + "/")}, secenekler...)...)
	c.uyu = func(_ context.Context, sure time.Duration) error {
		*beklemeler = append(*beklemeler, sure)
		return nil
	}
	return c
}

func hataAl(t *testing.T, err error) *Hata {
	t.Helper()
	var hata *Hata
	if !errors.As(err, &hata) {
		t.Fatalf("*Hata bekleniyordu: %v", err)
	}
	return hata
}

func esit(t *testing.T, beklenen, gercek any) {
	t.Helper()
	if !reflect.DeepEqual(beklenen, gercek) {
		t.Fatalf("beklenen %#v, gerçek %#v", beklenen, gercek)
	}
}

func TestZarfVeKota(t *testing.T) {
	s := sahteKur(t)
	var b []time.Duration
	sonuc, err := s.istemci(&b).Get(context.Background(), "/v1/doviz", map[string]string{"kod": "USD", "bos": ""})
	if err != nil {
		t.Fatal(err)
	}
	var veri struct {
		Kurlar []struct {
			Kod string `json:"kod"`
		} `json:"kurlar"`
	}
	if err := sonuc.Coz(&veri); err != nil {
		t.Fatal(err)
	}
	esit(t, "USD", veri.Kurlar[0].Kod)
	esit(t, "ecb", sonuc.Kaynak[0].Kod)
	esit(t, "2026-11-05T10:00:00", *sonuc.Guncelleme)
	esit(t, true, sonuc.Bayat)
	esit(t, 1000, *sonuc.Kota.GunlukLimit)
	esit(t, 999, *sonuc.Kota.GunlukKalan)
	esit(t, 20, *sonuc.Kota.DakikalikLimit)
	esit(t, 19, *sonuc.Kota.DakikalikKalan)
	esit(t, 1, *sonuc.Kota.IstekMaliyeti)
	istek := s.son()
	esit(t, "/v1/doviz", istek.URL.Path)
	esit(t, "kod=USD", istek.URL.RawQuery)
	esit(t, "va_test", istek.Header.Get("X-API-Key"))
}

func TestKisaYolVeZarfsizGovde(t *testing.T) {
	s := sahteKur(t, sahteYanit{durum: 200, govde: `{"durum":"ok"}`})
	var b []time.Duration
	sonuc, err := s.istemci(&b).Get(context.Background(), "durum", nil)
	if err != nil {
		t.Fatal(err)
	}
	esit(t, "/v1/durum", s.son().URL.Path)
	esit(t, `{"durum":"ok"}`, string(sonuc.Veri))
	esit(t, 0, len(sonuc.Kaynak))
	if sonuc.Kota.GunlukKalan != nil {
		t.Fatal("kota nil olmalıydı")
	}
}

func TestKolaylikYontemleri(t *testing.T) {
	s := sahteKur(t)
	var b []time.Duration
	c := s.istemci(&b)
	ctx := context.Background()
	denetle := func(err error, yol, sorgu string) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		istek := s.son()
		esit(t, yol, istek.URL.EscapedPath())
		esit(t, sorgu, istek.URL.Query().Encode())
	}
	_, err := c.Doviz(ctx, DovizParametreleri{Kod: []string{"USD", "EUR"}, Tur: "referans"})
	denetle(err, "/v1/doviz", "kod=USD%2CEUR&tur=referans")
	_, err = c.Hava(ctx, KonumParametreleri{Il: "Düzce", Ilce: "Akçakoca"})
	denetle(err, "/v1/hava", "il=D%C3%BCzce&ilce=Ak%C3%A7akoca")
	_, err = c.Hava(ctx, KonumParametreleri{Enlem: Sayi(0), Boylam: Sayi(32.5)})
	denetle(err, "/v1/hava", "boylam=32.5&enlem=0")
	_, err = c.Namaz(ctx, NamazParametreleri{KonumParametreleri: KonumParametreleri{Konum: "duzce-akcakoca"}, Tarih: "2026-11-05", Gun: 7})
	denetle(err, "/v1/namaz", "gun=7&konum=duzce-akcakoca&tarih=2026-11-05")
	_, err = c.Depremler(ctx, DepremParametreleri{Limit: 5, MinBuyukluk: Sayi(4.5), Saat: 24, Il: "81", Yurtici: true})
	denetle(err, "/v1/deprem/son", "il=81&limit=5&min_buyukluk=4.5&saat=24&yurtici=true")
	_, err = c.Iller(ctx)
	denetle(err, "/v1/konumlar/iller", "")
	_, err = c.Ilceler(ctx, "Düzce/x")
	denetle(err, "/v1/konumlar/iller/D%C3%BCzce%2Fx/ilceler", "")
}

func TestTipliHata(t *testing.T) {
	s := sahteKur(t, sahteYanit{durum: 404, basliklar: kotaBasliklari, govde: `{"hata":{"kod":404,"mesaj":"İl bulunamadı: x"}}`})
	var b []time.Duration
	_, err := s.istemci(&b).Ilceler(context.Background(), "x")
	hata := hataAl(t, err)
	esit(t, 404, hata.Kod)
	esit(t, "İl bulunamadı: x", hata.Mesaj)
	esit(t, 404, hata.HTTPDurumu)
	esit(t, "İl bulunamadı: x", hata.Error())
	esit(t, 999, *hata.Kota.GunlukKalan)
}

func Test422Alanlar(t *testing.T) {
	s := sahteKur(t, sahteYanit{durum: 422, govde: `{"hata":{"kod":422,"mesaj":{"metin":"Geçersiz parametre","alanlar":[{"alan":"gun","mesaj":"çok büyük"}]}}}`})
	var b []time.Duration
	_, err := s.istemci(&b).Namaz(context.Background(), NamazParametreleri{KonumParametreleri: KonumParametreleri{Il: "duzce"}, Gun: 99})
	hata := hataAl(t, err)
	esit(t, "Geçersiz parametre", hata.Mesaj)
	esit(t, []Alan{{Alan: "gun", Mesaj: "çok büyük"}}, hata.Alanlar)
}

func TestJSONOlmayanHata(t *testing.T) {
	s := sahteKur(t, sahteYanit{durum: 502, govde: "<html>Bad Gateway</html>"})
	var b []time.Duration
	_, err := s.istemci(&b).Iller(context.Background())
	hata := hataAl(t, err)
	esit(t, 502, hata.Kod)
	esit(t, 502, hata.HTTPDurumu)
}

func TestGecersizJSONBasari(t *testing.T) {
	s := sahteKur(t, sahteYanit{durum: 200, govde: "<html>"})
	var b []time.Duration
	_, err := s.istemci(&b).Iller(context.Background())
	hata := hataAl(t, err)
	esit(t, 0, hata.Kod)
	esit(t, 200, hata.HTTPDurumu)
}

func TestYonlendirmeIzlenmez(t *testing.T) {
	s := sahteKur(t, sahteYanit{durum: 302, basliklar: map[string]string{"Location": "/v1/baska"}})
	var b []time.Duration
	_, err := s.istemci(&b, WithHTTPClient(&http.Client{})).Iller(context.Background())
	esit(t, 302, hataAl(t, err).HTTPDurumu)
	esit(t, 1, s.adet())
}

func TestZamanAsimi(t *testing.T) {
	s := sahteKur(t, sahteYanit{durum: 200, govde: zarfMetni, gecikme: 2 * time.Second})
	var b []time.Duration
	_, err := s.istemci(&b, WithZamanAsimi(30*time.Millisecond)).Iller(context.Background())
	hata := hataAl(t, err)
	esit(t, true, hata.ZamanAsimi)
	esit(t, 0, hata.Kod)
	esit(t, 0, hata.HTTPDurumu)
}

func TestBaglantiHatasi(t *testing.T) {
	_, err := New("k", WithTemelAdres("http://127.0.0.1:9")).Iller(context.Background())
	hata := hataAl(t, err)
	esit(t, false, hata.ZamanAsimi)
	esit(t, 0, hata.HTTPDurumu)
}

func TestBosAnahtar(t *testing.T) {
	_, err := New("").Iller(context.Background())
	hataAl(t, err)
}

func sinir(retryAfter string) sahteYanit {
	basliklar := map[string]string{}
	if retryAfter != "" {
		basliklar["Retry-After"] = retryAfter
	}
	return sahteYanit{durum: 429, basliklar: basliklar, govde: `{"hata":{"kod":429,"mesaj":"Dakikalık istek sınırı aşıldı"}}`}
}

func TestRetryAfterUyulur(t *testing.T) {
	s := sahteKur(t, sinir("7"), sinir("3"))
	var b []time.Duration
	sonuc, err := s.istemci(&b).Iller(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	esit(t, true, sonuc.Bayat)
	esit(t, []time.Duration{7 * time.Second, 3 * time.Second}, b)
	esit(t, 3, s.adet())
}

func TestEnCokIkiDeneme(t *testing.T) {
	s := sahteKur(t, sinir("1"), sinir("1"), sinir("1"))
	var b []time.Duration
	_, err := s.istemci(&b).Iller(context.Background())
	hata := hataAl(t, err)
	esit(t, 429, hata.HTTPDurumu)
	esit(t, time.Second, hata.RetryAfter)
	esit(t, 3, s.adet())
}

func TestYenidenDenemeKapatilabilir(t *testing.T) {
	s := sahteKur(t, sinir("1"))
	var b []time.Duration
	_, err := s.istemci(&b, WithYenidenDeneme(0)).Iller(context.Background())
	hataAl(t, err)
	esit(t, 0, len(b))
	esit(t, 1, s.adet())
}

func TestUzunBeklemeDenenmez(t *testing.T) {
	s := sahteKur(t, sinir("40000"))
	var b []time.Duration
	_, err := s.istemci(&b).Iller(context.Background())
	esit(t, 40000*time.Second, hataAl(t, err).RetryAfter)
	esit(t, 0, len(b))
}

func TestBaslikYoksaVarsayilanBekleme(t *testing.T) {
	s := sahteKur(t, sinir(""))
	var b []time.Duration
	if _, err := s.istemci(&b).Iller(context.Background()); err != nil {
		t.Fatal(err)
	}
	esit(t, []time.Duration{time.Second}, b)
}

func TestBaskaHataDenenmez(t *testing.T) {
	s := sahteKur(t, sahteYanit{durum: 503, basliklar: map[string]string{"Retry-After": "1"}, govde: `{"hata":{"kod":503,"mesaj":"hazır değil"}}`})
	var b []time.Duration
	_, err := s.istemci(&b).Iller(context.Background())
	hataAl(t, err)
	esit(t, 1, s.adet())
}

func TestIptalBeklemeyiKeser(t *testing.T) {
	s := sahteKur(t, sinir("30"))
	ctx, iptal := context.WithCancel(context.Background())
	defer iptal()
	time.AfterFunc(20*time.Millisecond, iptal)
	_, err := New("k", WithTemelAdres(s.URL)).Iller(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("context.Canceled bekleniyordu: %v", err)
	}
	esit(t, 1, s.adet())
}

// Dört SDK'nın testlerinde aynı örnek
const (
	ornekGizli = "whsec_ornek"
	ornekZaman = "1762300800"
	ornekGovde = `{"olay":"deprem.yeni","veri":{"buyukluk":4.2,"il":"Düzce"}}`
	ornekImza  = "sha256=c7be673a84807aac83b2d3f90ef21166c532c8006043a2558cefe9c3d839edab"
)

var ornekSimdi = time.Unix(1762300900, 0)

func TestWebhookGecerli(t *testing.T) {
	esit(t, true, imzaDogrulaAn(ornekGizli, []byte(ornekGovde), ornekImza, ornekZaman, VarsayilanTolerans, ornekSimdi))
	buyuk := "sha256=" + strings.ToUpper(ornekImza[7:])
	esit(t, true, imzaDogrulaAn(ornekGizli, []byte(ornekGovde), buyuk, ornekZaman, VarsayilanTolerans, ornekSimdi))
}

func TestWebhookGecersiz(t *testing.T) {
	durumlar := []struct{ gizli, govde, imza, zaman string }{
		{ornekGizli, ornekGovde + " ", ornekImza, ornekZaman},
		{"baska", ornekGovde, ornekImza, ornekZaman},
		{"", ornekGovde, ornekImza, ornekZaman},
		{ornekGizli, ornekGovde, ornekImza, "1762300801"},
		{ornekGizli, ornekGovde, ornekImza[7:], ornekZaman},
		{ornekGizli, ornekGovde, "sha256=zz", ornekZaman},
		{ornekGizli, ornekGovde, "sha256=ğğ", ornekZaman},
		{ornekGizli, ornekGovde, "", ornekZaman},
		{ornekGizli, ornekGovde, ornekImza, ""},
	}
	for _, d := range durumlar {
		if imzaDogrulaAn(d.gizli, []byte(d.govde), d.imza, d.zaman, VarsayilanTolerans, ornekSimdi) {
			t.Fatalf("geçersiz sayılmalıydı: %+v", d)
		}
	}
}

func TestWebhookTolerans(t *testing.T) {
	gec := ornekSimdi.Add(300 * time.Second)
	esit(t, false, imzaDogrulaAn(ornekGizli, []byte(ornekGovde), ornekImza, ornekZaman, VarsayilanTolerans, gec))
	esit(t, true, imzaDogrulaAn(ornekGizli, []byte(ornekGovde), ornekImza, ornekZaman, 0, gec))
	esit(t, true, imzaDogrulaAn(ornekGizli, []byte(ornekGovde), ornekImza, ornekZaman, 1000*time.Second, gec))
}

func TestWebhookISOZaman(t *testing.T) {
	zaman := "2026-11-05T00:00:00+03:00"
	ozet := hmac.New(sha256.New, []byte("g"))
	ozet.Write([]byte(zaman + ".x"))
	imza := "sha256=" + hex.EncodeToString(ozet.Sum(nil))
	an := time.Unix(1793826000, 0)
	esit(t, true, imzaDogrulaAn("g", []byte("x"), imza, zaman, VarsayilanTolerans, an.Add(10*time.Second)))
	esit(t, false, imzaDogrulaAn("g", []byte("x"), imza, zaman, VarsayilanTolerans, an.Add(1000*time.Second)))
}
