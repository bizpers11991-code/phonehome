package web

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

func TestCompareDTO(t *testing.T) {
	to := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	p := model.Period{From: to.Add(-7 * 24 * time.Hour), To: to}
	tv := model.DeviceReport{
		Device: model.Device{ID: "mac:tv", Label: "TV"}, Period: p, PerDay: 80, Grade: "D",
		ByCategory: map[model.Category]int{model.CatAds: 560},
		Previous: &model.Comparison{
			Period: p.Previous(), Days: 7, Seen: true, PerDay: 1000, NowPerDay: 80, Grade: "F",
			ByCategory:    map[model.Category]int{model.CatACR: 6440, model.CatAds: 560},
			CategoryDelta: map[model.Category]float64{model.CatACR: -920, model.CatAds: 0},
			Stopped:       []model.Heartbeat{{Domain: "acr-us-prd.samsungcloud.tv", Category: model.CatACR, Every: time.Minute}},
		},
	}
	phone := model.DeviceReport{
		Device: model.Device{ID: "mac:phone", Label: "Phone"}, Period: p, PerDay: 5, Grade: "A",
		Previous: &model.Comparison{Period: p.Previous(), Days: 7, NowPerDay: 5},
	}
	r := model.HomeReport{
		Period: p, Devices: []model.DeviceReport{tv, phone}, Grade: "D",
		Previous: &model.HomeComparison{
			Comparison: model.Comparison{Period: p.Previous(), Days: 3.5, Partial: true, Seen: true, PerDay: 1010, NowPerDay: 85, Grade: "F"},
			Devices:    3,
			Gone:       []model.DeviceReport{{Device: model.Device{ID: "mac:lamp", Hostname: "lamp"}, Grade: "B", PerDay: 60}},
		},
	}
	b, err := json.Marshal(newReportDTO(r))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Previous struct {
			Days, DataDays, PerDay, NowPerDay float64
			Partial                           bool
			Change                            *float64
			Grade                             string
			Devices                           int
			Gone                              []struct{ ID, Name, Grade string }
		}
		Devices []struct {
			Previous *struct {
				Seen       bool
				Change     *float64
				Grade      string
				Stopped    []struct{ Domain, CategoryLabel string }
				Categories []struct {
					ID          string
					Before      int
					DeltaPerDay float64
				}
			}
		}
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	hp := got.Previous
	if hp.Days != 7 || hp.DataDays != 3.5 || !hp.Partial || hp.Grade != "F" || hp.Devices != 3 || hp.Change == nil {
		t.Errorf("home previous = %+v", hp)
	}
	if len(hp.Gone) != 1 || hp.Gone[0].Name != "lamp" || hp.Gone[0].Grade != "B" {
		t.Errorf("gone = %+v", hp.Gone)
	}
	d := got.Devices[0].Previous
	if d == nil || !d.Seen || d.Grade != "F" || d.Change == nil || *d.Change != -0.92 {
		t.Fatalf("TV previous = %+v", d)
	}
	if len(d.Stopped) != 1 || d.Stopped[0].CategoryLabel != "Content recognition" {
		t.Errorf("stopped = %+v", d.Stopped)
	}
	// Categories with nothing before and no change are left out.
	if len(d.Categories) != 2 || d.Categories[0].ID != "acr" || d.Categories[0].DeltaPerDay != -920 {
		t.Errorf("categories = %+v", d.Categories)
	}
	n := got.Devices[1].Previous
	if n == nil || n.Seen || n.Change != nil {
		t.Errorf("new device previous = %+v, want seen=false and change null", n)
	}
	if !strings.Contains(string(b), `"change":null`) {
		t.Error("an undefined change must be null, not 0")
	}

	// No comparison: the key is absent.
	r.Previous, r.Devices[0].Previous, r.Devices[1].Previous = nil, nil, nil
	b, _ = json.Marshal(newReportDTO(r))
	if strings.Contains(string(b), `"previous"`) {
		t.Errorf("report without a comparison has previous: %s", b)
	}
}
