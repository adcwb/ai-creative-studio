package publishing

import "testing"

func TestDeliveryRejectsUnconfirmedMalformedOrDuplicateReleaseFields(t *testing.T) {
	base := DeliveryInput{Title: " 正式片名 ", Tags: []string{"短剧"}, RightsConfirmed: true}
	for _, change := range []func(*DeliveryInput){func(v *DeliveryInput) { v.RightsConfirmed = false }, func(v *DeliveryInput) { v.ExpectedMetadataVersion = -1 }, func(v *DeliveryInput) { v.Title = " " }, func(v *DeliveryInput) { v.Title = "heading\nspoof" }, func(v *DeliveryInput) { v.Description = "bad\x00text" }, func(v *DeliveryInput) { v.Tags = []string{"Film", "film"} }, func(v *DeliveryInput) { v.Tags = []string{"a\nb"} }} {
		v := base
		change(&v)
		if _, e := NormalizeDelivery(v); e == nil {
			t.Fatal("accepted malformed release", v)
		}
	}
	v, e := NormalizeDelivery(base)
	if e != nil || v.Title != "正式片名" || base.Title != " 正式片名 " {
		t.Fatal(v, e)
	}
}
