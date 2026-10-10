package github

import "testing"

func TestParseURL(t *testing.T) {
	cases := []struct {
		in   string
		want Source
		err  bool
	}{
		{in: "https://github.com/Cp0204/CasaMOD", want: Source{Repo: "Cp0204/CasaMOD"}},
		{in: "github.com/Cp0204/CasaMOD.git", want: Source{Repo: "Cp0204/CasaMOD"}},
		{in: "Cp0204/CasaMOD", want: Source{Repo: "Cp0204/CasaMOD"}},
		{in: "https://github.com/Cp0204/CasaMOD/tree/main/app/mod/widget-sortable", want: Source{Repo: "Cp0204/CasaMOD", Branch: "main", Path: "app/mod/widget-sortable"}},
		{in: "https://github.com/Cp0204/CasaMOD/tree/main/app/mod/widget-sortable/?x=1#readme", want: Source{Repo: "Cp0204/CasaMOD", Branch: "main", Path: "app/mod/widget-sortable"}},
		{in: "https://github.com/Cp0204/CasaMOD/blob/main/app/mod/show-dotfiles/mod.js", want: Source{Repo: "Cp0204/CasaMOD", Branch: "main", Path: "app/mod/show-dotfiles"}},
		{in: "https://github.com/a/b/tree/dev", want: Source{Repo: "a/b", Branch: "dev"}},
		{in: "https://github.com/a/b/tree/main/my%20mod", want: Source{Repo: "a/b", Branch: "main", Path: "my mod"}},
		{in: "https://gitlab.com/a/b", err: true},
		{in: "https://github.com/a", err: true},
		{in: "https://github.com/a/b/issues/1", err: true},
		{in: "https://github.com/a/b/tree/main/../../etc", err: true},
	}
	for _, c := range cases {
		got, err := ParseURL(c.in)
		if c.err {
			if err == nil {
				t.Errorf("ParseURL(%q) = %+v, want error", c.in, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("ParseURL(%q) = %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
}

func TestSourceValidate(t *testing.T) {
	bad := []Source{{Repo: "nope"}, {Repo: "a/b/c"}, {Repo: "a/b", Path: "x/../.."}, {Repo: "a/b", Branch: "a..b"}}
	for _, s := range bad {
		if s.Validate() == nil {
			t.Errorf("%+v should be invalid", s)
		}
	}
	if err := (Source{Repo: "Owner-1/repo.name_2", Branch: "feature/x", Path: "a/b"}).Validate(); err != nil {
		t.Error(err)
	}
}
