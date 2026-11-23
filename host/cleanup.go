package main

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

const maxCleanupSelection = 32

type CleanupApp struct {
	PID             int    `json:"pid"`
	Name            string `json:"name"`
	Title           string `json:"title"`
	Resident        uint64 `json:"resident"`
	Committed       uint64 `json:"committed"`
	Processes       int    `json:"processes"`
	Windows         int    `json:"windows"`
	Closable        bool   `json:"closable"`
	ProtectedReason string `json:"protectedReason,omitempty"`
}

type CleanupOutcome struct {
	PID      int    `json:"pid"`
	Name     string `json:"name"`
	Title    string `json:"title"`
	Resident uint64 `json:"resident"`
	Status   string `json:"status"`
	Detail   string `json:"detail"`
}

type cleanupCloseRequest struct {
	Posted int
	Failed int
}

type CleanupState struct {
	Supported       bool             `json:"supported"`
	Busy            bool             `json:"busy"`
	Revision        int              `json:"revision"`
	CapturedAt      int64            `json:"capturedAt,omitempty"`
	Apps            []CleanupApp     `json:"apps,omitempty"`
	Outcomes        []CleanupOutcome `json:"outcomes,omitempty"`
	BeforeAvailable uint64           `json:"beforeAvailable,omitempty"`
	AfterAvailable  uint64           `json:"afterAvailable,omitempty"`
	Reclaimed       uint64           `json:"reclaimed,omitempty"`
	Requested       int              `json:"requested,omitempty"`
	Closed          int              `json:"closed,omitempty"`
	Remaining       int              `json:"remaining,omitempty"`
	Message         string           `json:"message,omitempty"`
	Error           string           `json:"error,omitempty"`
}

func cleanupProtection(name, title string, pid, self int, elevated bool) string {
	base := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), ".exe"))
	if pid <= 0 || pid == self || base == "yourlm" || strings.HasPrefix(base, "libylm_") || base == "llama-server" || base == "ggml-rpc-server" {
		return "YourLM i jego silnik są chronione"
	}
	if elevated {
		return "aplikacja działa jako administrator"
	}
	if strings.Contains(strings.ToLower(title), "yourlm") {
		return "to okno może zawierać panel YourLM"
	}
	protected := map[string]string{
		"explorer":                 "Pulpit i Eksplorator Windows są chronione",
		"dwm":                      "proces interfejsu Windows",
		"sihost":                   "proces interfejsu Windows",
		"shellexperiencehost":      "proces interfejsu Windows",
		"startmenuexperiencehost":  "proces interfejsu Windows",
		"searchhost":               "proces interfejsu Windows",
		"searchapp":                "proces interfejsu Windows",
		"textinputhost":            "proces interfejsu Windows",
		"applicationframehost":     "proces obsługujący aplikacje Windows",
		"runtimebroker":            "proces obsługujący aplikacje Windows",
		"systemsettings":           "Ustawienia Windows są chronione",
		"systemsettingsadminflows": "Ustawienia Windows są chronione",
		"lockapp":                  "proces interfejsu Windows",
		"logonui":                  "proces logowania Windows",
		"winlogon":                 "proces logowania Windows",
		"taskhostw":                "proces obsługujący Windows",
		"backgroundtaskhost":       "proces obsługujący Windows",
		"dllhost":                  "proces obsługujący Windows",
		"useroobebroker":           "proces obsługujący Windows",
		"widgets":                  "proces interfejsu Windows",
		"widgetservice":            "proces interfejsu Windows",
		"taskmgr":                  "narzędzie systemowe Windows",
		"regedit":                  "narzędzie systemowe Windows",
		"mmc":                      "narzędzie systemowe Windows",
		"msiexec":                  "trwający instalator jest chroniony",
		"consent":                  "okno zabezpieczeń Windows",
		"smartscreen":              "Zabezpieczenia Windows są chronione",
		"securityhealthsystray":    "Zabezpieczenia Windows są chronione",
		"sechealthui":              "Zabezpieczenia Windows są chronione",
		"msmpeng":                  "Microsoft Defender jest chroniony",
		"nissrv":                   "Microsoft Defender jest chroniony",
		"msseces":                  "oprogramowanie zabezpieczające jest chronione",
		"avastui":                  "oprogramowanie zabezpieczające jest chronione",
		"avgui":                    "oprogramowanie zabezpieczające jest chronione",
		"avpui":                    "oprogramowanie zabezpieczające jest chronione",
		"egui":                     "oprogramowanie zabezpieczające jest chronione",
		"mbam":                     "oprogramowanie zabezpieczające jest chronione",
		"mbamtray":                 "oprogramowanie zabezpieczające jest chronione",
		"nortonsecurity":           "oprogramowanie zabezpieczające jest chronione",
		"mcuihost":                 "oprogramowanie zabezpieczające jest chronione",
		"bdagent":                  "oprogramowanie zabezpieczające jest chronione",
		"sophosui":                 "oprogramowanie zabezpieczające jest chronione",
		"cmd":                      "terminal może wykonywać ważne zadanie",
		"powershell":               "terminal może wykonywać ważne zadanie",
		"pwsh":                     "terminal może wykonywać ważne zadanie",
		"windowsterminal":          "terminal może wykonywać ważne zadanie",
		"openconsole":              "terminal może wykonywać ważne zadanie",
		"wezterm-gui":              "terminal może wykonywać ważne zadanie",
		"alacritty":                "terminal może wykonywać ważne zadanie",
		"kitty":                    "terminal może wykonywać ważne zadanie",
		"mintty":                   "terminal może wykonywać ważne zadanie",
		"putty":                    "terminal może wykonywać ważne zadanie",
		"conemu":                   "terminal może wykonywać ważne zadanie",
		"conemu64":                 "terminal może wykonywać ważne zadanie",
		"conhost":                  "konsola systemowa jest chroniona",
		"mstsc":                    "zamknięcie mogłoby przerwać zdalną sesję",
		"msrdc":                    "zamknięcie mogłoby przerwać zdalną sesję",
		"rdcman":                   "zamknięcie mogłoby przerwać zdalną sesję",
		"vmconnect":                "zamknięcie mogłoby przerwać zdalną sesję",
	}
	if reason := protected[base]; reason != "" {
		return reason
	}
	securityText := base + " " + strings.ToLower(title)
	for _, marker := range []string{"antivirus", "anti-virus", "defender", "endpoint security", "internet security", "zabezpieczenia windows"} {
		if strings.Contains(securityText, marker) {
			return "oprogramowanie zabezpieczające jest chronione"
		}
	}
	if strings.Contains(base, "setup") || strings.Contains(base, "install") || strings.Contains(base, "unins") || strings.Contains(base, "update") {
		return "instalatory i aktualizatory są chronione"
	}
	return ""
}

func matchCleanupSelection(cached, current []CleanupApp, pids []int) ([]CleanupApp, error) {
	if len(pids) == 0 {
		return nil, errors.New("zaznacz co najmniej jedną aplikację")
	}
	if len(pids) > maxCleanupSelection {
		return nil, fmt.Errorf("można zamknąć najwyżej %d aplikacje naraz", maxCleanupSelection)
	}
	cachedByPID := make(map[int]CleanupApp, len(cached))
	for _, app := range cached {
		cachedByPID[app.PID] = app
	}
	currentByPID := make(map[int]CleanupApp, len(current))
	for _, app := range current {
		currentByPID[app.PID] = app
	}
	seen := make(map[int]bool, len(pids))
	selected := make([]CleanupApp, 0, len(pids))
	for _, pid := range pids {
		if pid <= 0 || seen[pid] {
			return nil, errors.New("niepoprawna lista aplikacji")
		}
		seen[pid] = true
		old, wasListed := cachedByPID[pid]
		now, stillListed := currentByPID[pid]
		if !wasListed || !stillListed || !old.Closable || !now.Closable || !strings.EqualFold(old.Name, now.Name) {
			return nil, errors.New("lista aplikacji zmieniła się — odśwież ją przed zamknięciem")
		}
		selected = append(selected, now)
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Resident > selected[j].Resident })
	return selected, nil
}

func cleanupOutcomes(selected, after []CleanupApp, requests map[int]cleanupCloseRequest) ([]CleanupOutcome, int, int, int) {
	byPID := make(map[int]CleanupApp, len(after))
	for _, app := range after {
		byPID[app.PID] = app
	}
	outcomes := make([]CleanupOutcome, 0, len(selected))
	requested, closed, remaining := 0, 0, 0
	for _, app := range selected {
		request := requests[app.PID]
		if request.Posted > 0 {
			requested++
		}
		current, stillOpen := byPID[app.PID]
		stillOpen = stillOpen && strings.EqualFold(current.Name, app.Name)
		outcome := CleanupOutcome{
			PID: app.PID, Name: app.Name, Title: app.Title, Resident: app.Resident,
		}
		switch {
		case stillOpen && request.Posted > 0:
			remaining++
			outcome.Status = "remaining"
			outcome.Detail = "Nadal działa — może czekać na zapisanie dokumentu lub anulowano zamknięcie."
		case stillOpen:
			remaining++
			outcome.Status = "not-requested"
			outcome.Detail = "Windows nie przyjął prośby o zamknięcie żadnego okna tej aplikacji."
		case request.Posted > 0:
			closed++
			outcome.Status = "closed"
			outcome.Detail = "Zniknęła z listy po łagodnej prośbie o zamknięcie."
		default:
			closed++
			outcome.Status = "closed-unconfirmed"
			outcome.Detail = "Zniknęła podczas ponownego sprawdzania; wysłania prośby nie potwierdzono."
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes, requested, closed, remaining
}

func (a *App) cleanupFailure(w http.ResponseWriter, err error, status int) {
	a.Lock()
	a.cleanupBusy = false
	a.cleanup.Busy = false
	a.cleanup.Error = err.Error()
	a.cleanup.Message = ""
	a.cleanup.Revision++
	a.Unlock()
	http.Error(w, err.Error(), status)
}

func (a *App) cleanupStateResponse(w http.ResponseWriter) {
	a.Lock()
	defer a.Unlock()
	respond(w, a.state())
}

func (a *App) cleanupAPI(w http.ResponseWriter, operation string, in input) {
	if !cleanupPlatformSupported() {
		a.cleanupFailure(w, errors.New("bezpieczne zamykanie aplikacji jest dostępne tylko na Windows"), http.StatusBadRequest)
		return
	}
	a.Lock()
	if a.cleanupBusy {
		a.Unlock()
		http.Error(w, "sprawdzanie aplikacji już trwa", http.StatusConflict)
		return
	}
	a.cleanupBusy = true
	a.cleanup.Busy = true
	a.cleanup.Error = ""
	a.cleanup.Message = ""
	a.cleanup.Outcomes = nil
	a.cleanup.BeforeAvailable = 0
	a.cleanup.AfterAvailable = 0
	a.cleanup.Reclaimed = 0
	a.cleanup.Requested = 0
	a.cleanup.Closed = 0
	a.cleanup.Remaining = 0
	cached := append([]CleanupApp(nil), a.cleanup.Apps...)
	sessionActive := a.s != nil
	a.Unlock()

	switch operation {
	case "cleanup-scan":
		apps, err := scanCleanupApps()
		if err != nil {
			a.cleanupFailure(w, err, http.StatusInternalServerError)
			return
		}
		a.Lock()
		a.cleanupBusy = false
		a.cleanup = CleanupState{
			Supported:  true,
			Revision:   a.cleanup.Revision + 1,
			CapturedAt: time.Now().UnixMilli(),
			Apps:       apps,
			Message:    "Największe zwykłe aplikacje zaznaczono wstępnie. Odznacz wyjątki, zapisz pracę i wyślij łagodną prośbę o zamknięcie.",
		}
		a.Unlock()
		a.cleanupStateResponse(w)

	case "cleanup-close":
		if sessionActive {
			a.cleanupFailure(w, errors.New("zatrzymaj model przed zwalnianiem pamięci"), http.StatusConflict)
			return
		}
		current, err := scanCleanupApps()
		if err != nil {
			a.cleanupFailure(w, err, http.StatusInternalServerError)
			return
		}
		selected, err := matchCleanupSelection(cached, current, in.PIDs)
		if err != nil {
			a.cleanupFailure(w, err, http.StatusBadRequest)
			return
		}
		before := systemMemory().Available
		// Recheck immediately before touching another application and hold the
		// app lock while posting messages. A concurrent model start either wins
		// before this check (and aborts cleanup) or waits until cleanup is marked
		// complete; the two operations can never begin together.
		a.Lock()
		if a.s != nil {
			a.Unlock()
			a.cleanupFailure(w, errors.New("model uruchomił się podczas sprawdzania — zatrzymaj go i spróbuj ponownie"), http.StatusConflict)
			return
		}
		requests, closeErr := requestCloseCleanupApps(selected)
		a.Unlock()
		// WM_CLOSE is asynchronous. Give ordinary applications time to exit or
		// display their own save confirmation; YourLM never force-terminates them.
		time.Sleep(1800 * time.Millisecond)
		afterApps, scanErr := scanCleanupApps()
		after := systemMemory().Available
		if scanErr != nil {
			a.cleanupFailure(w, scanErr, http.StatusInternalServerError)
			return
		}
		outcomes, requested, closed, remaining := cleanupOutcomes(selected, afterApps, requests)
		reclaimed := uint64(0)
		if after > before {
			reclaimed = after - before
		}
		message := fmt.Sprintf("Wysłano łagodne zamknięcie do %d z %d aplikacji; %d zniknęło z listy.", requested, len(selected), closed)
		if remaining > 0 {
			message += fmt.Sprintf(" %d nadal działa — sprawdź, czy nie czeka na zapisanie dokumentu.", remaining)
		}
		if closeErr != nil {
			message += " Nie wszystkie okna przyjęły prośbę: " + closeErr.Error()
		}
		a.Lock()
		a.cleanupBusy = false
		a.cleanup = CleanupState{
			Supported:       true,
			Revision:        a.cleanup.Revision + 1,
			CapturedAt:      time.Now().UnixMilli(),
			Apps:            afterApps,
			Outcomes:        outcomes,
			BeforeAvailable: before,
			AfterAvailable:  after,
			Reclaimed:       reclaimed,
			Requested:       requested,
			Closed:          closed,
			Remaining:       remaining,
			Message:         message,
		}
		a.Unlock()
		a.cleanupStateResponse(w)

	default:
		a.cleanupFailure(w, errors.New("nieznana operacja pamięci"), http.StatusNotFound)
	}
}
