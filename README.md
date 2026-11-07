# VeriApi.Pro Go SDK: döviz kuru, namaz vakti, hava durumu ve deprem API'si

Go ile döviz kurlarını, namaz vakitlerini, hava durumunu, son depremleri ve il/ilçe listesini tek API anahtarıyla çekin.

Official Go client for [VeriApi.Pro](https://veriapi.pro/?ref=github): Turkish exchange rates, prayer times, weather, earthquakes and cities.

Go 1.21+, yalnızca standart kitaplık.

```bash
go get github.com/veriapipro/veriapi-go
```

## Hızlı başlangıç

```go
package main

import (
	"context"
	"fmt"
	"log"

	veriapi "github.com/veriapipro/veriapi-go"
)

func main() {
	api := veriapi.New("va_ANAHTARINIZ")
	sonuc, err := api.Doviz(context.Background(), veriapi.DovizParametreleri{Kod: []string{"USD", "EUR"}})
	if err != nil {
		log.Fatal(err)
	}

	var veri struct {
		Kurlar []map[string]any `json:"kurlar"`
	}
	if err := sonuc.Coz(&veri); err != nil {
		log.Fatal(err)
	}
	fmt.Println(veri.Kurlar)
	fmt.Println(*sonuc.Kaynak[0].Atif) // veriyi gösterdiğiniz yerde görünür yazın
	fmt.Println(sonuc.Bayat)
	if sonuc.Kota.GunlukKalan != nil {
		fmt.Println(*sonuc.Kota.GunlukKalan)
	}
}
```

`Sonuc.Veri` ham JSON'dur (`json.RawMessage`), `Coz` kendi tipinize çözer. `Guncelleme` ve kota alanları yoksa `nil`dır.

## Yöntemler

```go
ctx := context.Background()

api.Doviz(ctx, veriapi.DovizParametreleri{Kod: []string{"USD"}, Tur: "referans"}) // Tur: anlik | referans
api.Hava(ctx, veriapi.KonumParametreleri{Il: "duzce", Ilce: "akcakoca"})
api.Hava(ctx, veriapi.KonumParametreleri{Enlem: veriapi.Sayi(40.9), Boylam: veriapi.Sayi(31.1)})
api.Namaz(ctx, veriapi.NamazParametreleri{
	KonumParametreleri: veriapi.KonumParametreleri{Il: "ankara"},
	Tarih:              "2026-11-05",
	Gun:                7,
})
api.Depremler(ctx, veriapi.DepremParametreleri{Limit: 10, MinBuyukluk: veriapi.Sayi(4), Saat: 24, Il: "izmir", Yurtici: true})
api.Iller(ctx)
api.Ilceler(ctx, "duzce") // ad, slug ya da plaka

api.Get(ctx, "/v1/takvim/tatiller", map[string]string{"yil": "2026"}) // diğer tüm uçlar
```

## Seçenekler

```go
api := veriapi.New("va_ANAHTARINIZ",
	veriapi.WithZamanAsimi(10*time.Second),   // istek başına
	veriapi.WithYenidenDeneme(2),             // 429'da en çok kaç kez daha; 0 kapatır
	veriapi.WithAzamiBekleme(60*time.Second), // bundan uzun Retry-After beklenmez, hata hemen döner
)
```

`ctx` iptali istekleri ve 429 beklemesini keser. `WithHTTPClient` ile özel `http.Client` verilebilir; yönlendirmeler yine izlenmez.

## Hatalar

```go
_, err := api.Ilceler(ctx, "yok")
var hata *veriapi.Hata
if errors.As(err, &hata) {
	fmt.Println(hata.Kod, hata.Mesaj, hata.HTTPDurumu)
	fmt.Println(hata.Alanlar)    // 422
	fmt.Println(hata.RetryAfter) // 429
	fmt.Println(hata.ZamanAsimi) // zaman aşımında true; bağlantı hatasında Kod ve HTTPDurumu 0
}
```

## Webhook doğrulama

Ham gövdeyi kullanın; JSON'u ayrıştırıp yeniden üretirseniz imza tutmaz.

```go
http.HandleFunc("/webhook", func(w http.ResponseWriter, r *http.Request) {
	govde, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "okunamadı", http.StatusBadRequest)
		return
	}
	gecerli := veriapi.ImzaDogrula("WEBHOOK_GIZLI_ANAHTARINIZ", govde,
		r.Header.Get("X-VeriApi-Imza"), r.Header.Get("X-VeriApi-Zaman"), veriapi.VarsayilanTolerans)
	if !gecerli {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	fmt.Println(r.Header.Get("X-VeriApi-Olay"), string(govde))
	w.WriteHeader(http.StatusNoContent)
})
```

Son parametre zaman farkı sınırıdır; `0` denetimi kapatır.

## Test

```bash
go test ./...
```

## Bağlantılar

- Ücretsiz anahtar ve canlı deneme: <https://veriapi.pro/?ref=github>
- Dokümantasyon: <https://veriapi.pro/dokumantasyon>
- OpenAPI: <https://api.veriapi.pro/v1/openapi.json>

Lisans: MIT
