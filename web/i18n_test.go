package web

import (
	"encoding/json"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The 31 CasaOS languages (src/assets/lang of CasaOS-UI).
var casaOSLangs = strings.Fields(`ar_sa be_by bg_bg ca_es cs_cz de_de el_gr en_us es_es fr_fr
	hr_hr hu_hu id_id it_it ja_jp ko_kr nl_nl no_no pl_pl pt_br pt_pt ro_ro ru_ru sk_sk sl_si sv_se
	tr_tr uk_ua vi_vn zh_cn zh_tw`)

var placeholder = regexp.MustCompile(`\{\w+\}`)

func load(t *testing.T, code string) map[string]string {
	t.Helper()
	data, err := fs.ReadFile(FS, "lang/"+code+".json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("%s: %v", code, err)
	}
	return m
}

func placeholders(s string) string {
	p := placeholder.FindAllString(s, -1)
	sort.Strings(p)
	return strings.Join(p, ",")
}

// Every language has every text of the English file, with the same
// placeholders, and nothing else.
func TestLanguagesAreComplete(t *testing.T) {
	files, _ := fs.Glob(FS, "lang/*.json")
	if len(files) != len(casaOSLangs) {
		t.Fatalf("%d language files, want %d", len(files), len(casaOSLangs))
	}
	en := load(t, "en_us")
	for _, code := range casaOSLangs {
		m := load(t, code)
		for k, v := range en {
			got, ok := m[k]
			if !ok || strings.TrimSpace(got) == "" {
				t.Errorf("%s: missing %q", code, k)
				continue
			}
			if placeholders(got) != placeholders(v) {
				t.Errorf("%s: %q has placeholders %q, want %q", code, k, placeholders(got), placeholders(v))
			}
		}
		for k := range m {
			if _, ok := en[k]; !ok {
				t.Errorf("%s: unknown key %q", code, k)
			}
		}
	}
}

// Every key used by the page exists in English, and app.js lists exactly
// the language files.
func TestKeysUsedByThePage(t *testing.T) {
	en := load(t, "en_us")
	js, _ := fs.ReadFile(FS, "app.js")
	html, _ := fs.ReadFile(FS, "index.html")
	used := regexp.MustCompile(`\bt\("([a-z_]+\.[A-Za-z_]+)"`).FindAllStringSubmatch(string(js), -1)
	used = append(used, regexp.MustCompile(`data-i18n(?:-placeholder)?="([^"]+)"`).FindAllStringSubmatch(string(html), -1)...)
	for _, m := range used {
		if _, ok := en[m[1]]; !ok {
			t.Errorf("key %q is used but not defined", m[1])
		}
	}
	for _, code := range casaOSLangs {
		if !strings.Contains(string(js), `"`+code+`"`) {
			t.Errorf("app.js does not list %s", code)
		}
	}
}
