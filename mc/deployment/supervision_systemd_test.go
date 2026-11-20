package deployment

import (
	"path/filepath"
	"strings"
	"testing"
)

// The systemd renderers are untagged on purpose: these tests run on any host
// and pin the unit-file shape, the quoting rules, and the refusal of control
// characters in paths.

func TestSystemdQuoteValue(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`plain`, `"plain"`},
		{`has space`, `"has space"`},
		{`say "hi"`, `"say \"hi\""`},
		{`back\slash`, `"back\\slash"`},
	} {
		got, err := systemdQuoteValue(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("quote %q = %q %v, want %q", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{"line\nbreak", "carriage\rreturn", "nul\x00value"} {
		if _, err := systemdQuoteValue(bad); err == nil {
			t.Fatalf("control value %q accepted", bad)
		}
	}
}

func TestRenderSystemdUnitShape(t *testing.T) {
	body, err := renderSystemdUnitChecked(systemdUnit{
		Description:      "Culler resident (com.culler.resident-ab12cd34ef56)",
		ExecStart:        []string{"/usr/local/bin/bun", "/mc/home/release/host/resident/src/main.ts", "--config", "/mc/home/supervision/resident.json"},
		Environment:      [][2]string{{"MC_HOME", "/mc/home"}, {"PATH", "/a:/b"}},
		WorkingDirectory: "/mc/home/release/host/resident",
		StandardOutput:   "/mc/home/logs/resident.stdout.log",
		StandardError:    "/mc/home/logs/resident.stderr.log",
	})
	if err != nil {
		t.Fatal(err)
	}
	// The whole file, byte for byte. A Contains list cannot see the section a
	// key lands in, and the first acceptance run on the designated host proved
	// that: an ExecStart emitted between [Unit] and [Service] passed every
	// fragment assertion and was refused by systemd itself.
	got := string(body)
	want := "[Unit]\n" +
		"Description=\"Culler resident (com.culler.resident-ab12cd34ef56)\"\n" +
		"\n" +
		"[Service]\n" +
		"ExecStart=\"/usr/local/bin/bun\" \"/mc/home/release/host/resident/src/main.ts\" \"--config\" \"/mc/home/supervision/resident.json\"\n" +
		"WorkingDirectory=/mc/home/release/host/resident\n" +
		"Environment=\"MC_HOME=/mc/home\"\n" +
		"Environment=\"PATH=/a:/b\"\n" +
		"StandardOutput=append:/mc/home/logs/resident.stdout.log\n" +
		"StandardError=append:/mc/home/logs/resident.stderr.log\n" +
		"Restart=always\n" +
		"RestartSec=10\n" +
		"\n" +
		"[Install]\n" +
		"WantedBy=default.target\n"
	if got != want {
		t.Fatalf("unit bytes differ:\ngot:\n%s\nwant:\n%s", got, want)
	}
	// WorkingDirectory renders raw (systemd 249 keeps the quotes as path
	// characters and rejects the unit), so a path that cannot survive raw form
	// is a refusal, not a corrupt file.
	for _, bad := range []string{`/mc/ho"me`, `/mc/ho\me`} {
		u := systemdUnit{
			Description: "d", ExecStart: []string{"/bin/x"},
			WorkingDirectory: bad,
		}
		if _, err := renderSystemdUnitChecked(u); err == nil {
			t.Fatalf("WorkingDirectory %q accepted", bad)
		}
	}
}

func TestSystemdBundledUnitsPathsAndSpaces(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc home") // a space in the deployment home
	names := Names{Suffix: "ab12cd34ef56", Volume: "mc-spine-ab12cd34ef56", RuntimeVolume: "mc-runtime-ab12cd34ef56", Helper: "mc-helper-ab12cd34ef56"}
	in := unitRenderInput{
		Result:          SupervisionResultFor(home, names),
		CanonicalHome:   home,
		Names:           names,
		Spec:            SupervisionSpec{SupervisionPolicy: SupervisionPolicy{AgentsViewBind: "127.0.0.1:7334"}},
		MCPath:          "/usr/local/bin/mc",
		BunPath:         "/usr/local/bin/bun",
		HostRoot:        filepath.Join(home, "release", "host"),
		PathEnv:         "/usr/local/bin:/usr/bin:/bin",
		LogRoot:         filepath.Join(home, "logs"),
		AgentsViewToken: strings.Repeat("a", 64),
	}
	files, err := systemdBundledUnits(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"units/resident.service", "units/dashboard.service", "units/agentsview.service", agentsViewConfig} {
		if _, ok := files[rel]; !ok {
			t.Fatalf("bundle missing %q: %v", rel, files)
		}
	}
	resident := string(files["units/resident.service"])
	if !strings.Contains(resident, `Environment="MC_HOME=`+home+`"`) {
		t.Fatalf("spaced MC_HOME not quoted into the unit:\n%s", resident)
	}
	// A control character anywhere in the render input is a refusal, not a
	// silently corrupt unit file.
	bad := in
	bad.CanonicalHome = home + "\n"
	if _, err := systemdBundledUnits(bad); err == nil {
		t.Fatal("control character in home accepted")
	}
}
