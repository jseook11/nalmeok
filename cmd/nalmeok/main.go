package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jseook11/nalmeok/frontend"
	"github.com/jseook11/nalmeok/internal/app"
	"github.com/jseook11/nalmeok/internal/codexasr"
	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/gemini"
	"github.com/jseook11/nalmeok/internal/i18n"
	"github.com/jseook11/nalmeok/internal/macdrop"
	"github.com/jseook11/nalmeok/internal/notify"
	"github.com/jseook11/nalmeok/internal/runner"
	"github.com/jseook11/nalmeok/internal/store"
	"github.com/jseook11/nalmeok/internal/telemetry"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// version is overridden at build time with
// -ldflags "-X main.version=...".
var version = "0.10.3-dev"

func main() {
	// The GUI has no reliable locale in its environment when launched from
	// Finder or the Dock; the window reports the OS language back through
	// App.UseLanguage once it loads. This covers the CLI and early messages.
	_ = i18n.SetLanguage(i18n.Detect(systemLocale()))

	args := filterPSNArgs(os.Args[1:])
	if len(args) == 0 {
		runGUI()
		return
	}
	// Windows: the exe is a GUI program, so its output goes nowhere unless
	// it joins the console it was started from. No-op elsewhere.
	attachParentConsole()

	switch args[0] {
	case "--version", "-version":
		fmt.Println(version)
	case "doctor":
		os.Exit(runDoctor())
	default:
		os.Exit(runCLI(args))
	}
}

// systemLocale returns the locale the OS reports through the environment.
// GUI processes may not have one; i18n.Detect keeps the base language then.
func systemLocale() string {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANGUAGE", "LANG"} {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			continue
		}
		// LANGUAGE is a priority list ("ko:en"); the first entry decides.
		if name == "LANGUAGE" {
			value = strings.TrimSpace(strings.SplitN(value, ":", 2)[0])
		}
		if value != "" {
			return value
		}
	}
	return ""
}

// filterPSNArgs drops the process serial number arguments macOS adds when the
// app is launched from Finder or the Dock. Without this, every GUI launch
// would look like a command-line invocation.
func filterPSNArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.HasPrefix(arg, "-psn_") {
			continue
		}
		out = append(out, arg)
	}
	return out
}

// sharedRuntime holds what the GUI and CLI load once: settings, history, and
// the health-signal client. The GUI builds one runner per backend from it.
type sharedRuntime struct {
	cfg            *config.Config
	store          *store.Store
	signals        *telemetry.Client
	workDir        string
	transcriptsDir string
	logPath        string
}

// newSharedRuntime loads the pieces both entry points share and starts the
// signals client. Callers close signals.
func newSharedRuntime() (*sharedRuntime, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	itemsPath, err := config.ItemsPath()
	if err != nil {
		return nil, err
	}
	history, err := store.Load(itemsPath)
	if err != nil {
		return nil, err
	}
	workDir, err := config.WorkDir()
	if err != nil {
		return nil, err
	}
	transcriptsDir, err := config.TranscriptsDir()
	if err != nil {
		return nil, err
	}
	logPath, err := config.LogPath()
	if err != nil {
		return nil, err
	}
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	signals := telemetry.New(telemetry.Options{
		App:     version,
		Dir:     dir,
		Enabled: signalsEnabled,
	})
	signals.Start()
	return &sharedRuntime{
		cfg:            cfg,
		store:          history,
		signals:        signals,
		workDir:        workDir,
		transcriptsDir: transcriptsDir,
		logPath:        logPath,
	}, nil
}

// newRunner builds one runner. Each backend knows its own piece length and
// pacing; the two env knobs only override them for an experiment run. 0 means
// "backend default".
func (s *sharedRuntime) newRunner(backend string) *runner.Runner {
	opts := runner.Options{
		Store:          s.store,
		Media:          runner.DefaultMedia{},
		NewClient:      gemini.New,
		Backend:        backend,
		WorkDir:        s.workDir,
		TranscriptsDir: s.transcriptsDir,
		LogPath:        s.logPath,
		Notify:         notify.Send,
		Signal:         s.signals.Record,
	}
	if backend == runner.BackendCodex {
		opts.Codex = runner.CodexOptions{
			Concurrency: envInt("NALMEOK_CODEX_CONCURRENCY"),
		}
		opts.UnitMaxSeconds = float64(envInt("NALMEOK_CODEX_UNIT_SECONDS"))
	}
	return runner.New(opts)
}

// newRuntime wires one runner for the CLI. backend is the flag or environment
// value; when it is empty and useConfig is true, config.json's choice applies.
func newRuntime(backend string, useConfig bool) (*config.Config, *store.Store, *runner.Runner, *telemetry.Client, error) {
	s, err := newSharedRuntime()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if useConfig && strings.TrimSpace(backend) == "" {
		backend = s.cfg.Backend()
	}
	backend, err = normalizeBackend(backend)
	if err != nil {
		s.signals.Close()
		return nil, nil, nil, nil, err
	}
	return s.cfg, s.store, s.newRunner(backend), s.signals, nil
}

// backendEnv is the environment variable the GUI reads, since a window
// launched from Finder gets no command line.
const backendEnv = "NALMEOK_BACKEND"

// experimentsEnv lists enabled experiments, comma separated, for a build that
// should show the setting without editing config.json.
const experimentsEnv = "NALMEOK_EXPERIMENTS"

// normalizeBackend validates a backend name and maps the default spellings to
// the empty string.
func normalizeBackend(backend string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "", "gemini":
		return "", nil
	case runner.BackendCodex:
		return runner.BackendCodex, nil
	}
	return "", fmt.Errorf("unknown backend %q: use gemini or codex", backend)
}

// codexExperiment reports whether the hidden Codex setting is enabled, from
// config.json or the environment.
func codexExperiment(cfg *config.Config) bool {
	if cfg != nil && cfg.Experiment("codex") {
		return true
	}
	for _, name := range strings.Split(os.Getenv(experimentsEnv), ",") {
		if strings.EqualFold(strings.TrimSpace(name), "codex") {
			return true
		}
	}
	return false
}

// envInt reads a positive integer experiment knob. An absent or invalid
// value is 0, which means "use the default".
func envInt(name string) int {
	v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || v <= 0 {
		return 0
	}
	return v
}

// signalsEnabled re-reads config.json, so turning signals off in settings
// (or in another process) applies to the next event. The file is written
// atomically, so a read never sees half a file. config.Load treats an
// unreadable file as empty (signals on), so a file that exists but cannot be
// read right now (Windows sharing during a save) sends nothing instead.
func signalsEnabled() bool {
	path, err := config.ConfigPath()
	if err != nil {
		return false
	}
	if _, err := os.ReadFile(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	cfg, err := config.Load()
	if err != nil {
		return false
	}
	return cfg.SignalsEnabled()
}

func runGUI() {
	// A second app window is handed to the first by Wails' single-instance
	// lock below. The folder lock also keeps the app from running next to a
	// CLI run on the same items.json; if the CLI holds it, the window only
	// says so and closes.
	folderLock, lockErr := config.Lock()
	if lockErr == nil {
		defer folderLock.Close()
	}

	forced := strings.TrimSpace(os.Getenv(backendEnv))
	shared, err := newSharedRuntime()
	if err != nil {
		log.Fatal(err)
	}
	// Sends what is still queued, waiting at most 2 s.
	defer shared.signals.Close()

	startupBackend := forced
	if startupBackend == "" {
		startupBackend = shared.cfg.Backend()
	}
	startupBackend, err = normalizeBackend(startupBackend)
	if err != nil {
		log.Fatal(err)
	}
	forcedSet := forced != ""
	currentBackend := func() string {
		if forcedSet {
			if startupBackend == runner.BackendCodex {
				return config.BackendCodex
			}
			return config.BackendGemini
		}
		return shared.cfg.Backend()
	}

	var application *app.App
	var onDomReady func(context.Context)
	appearance := mac.DefaultAppearance
	if os.Getenv("NALMEOK_UI_PREVIEW") == "1" {
		// Screenshot mode: the saved items stay as they are and nothing
		// touches the network. See docs/development.md.
		application = app.New(shared.cfg, shared.store, app.NewStubRunner(shared.store))
		switch os.Getenv("NALMEOK_UI_APPEARANCE") {
		case "light":
			appearance = mac.NSAppearanceNameAqua
		case "dark":
			appearance = mac.NSAppearanceNameDarkAqua
		}
		if js := os.Getenv("NALMEOK_UI_PREVIEW_JS"); js != "" {
			onDomReady = func(ctx context.Context) {
				go func() {
					time.Sleep(time.Second)
					wailsruntime.WindowExecJS(ctx, js)
				}()
			}
		}
	} else {
		// Two runners, one per backend: changing the setting routes new items
		// without a restart, while items already added keep their backend.
		router := &backendRouter{
			gemini:  shared.newRunner(""),
			codex:   shared.newRunner(runner.BackendCodex),
			store:   shared.store,
			current: currentBackend,
		}
		defer router.Close()
		application = app.New(shared.cfg, shared.store, router)
		application.ValidateKey = gemini.ValidateKey
		application.Telemetry = shared.signals
	}
	application.BackendFunc = currentBackend
	application.BackendForced = forcedSet
	application.Experiment = codexExperiment(shared.cfg)
	application.TranscriptsDir = shared.transcriptsDir

	assets, err := fs.Sub(frontend.Assets, "dist")
	if err != nil {
		log.Fatal(err)
	}

	// Set on startup, read when a second launch asks for the window.
	var windowCtx atomic.Pointer[context.Context]
	onStartup := func(ctx context.Context) {
		windowCtx.Store(&ctx)
		if lockErr != nil {
			_, _ = wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
				Type:    wailsruntime.ErrorDialog,
				Title:   "Nalmeok",
				Message: lockMessage(lockErr),
			})
			wailsruntime.Quit(ctx)
			return
		}
		application.Startup(ctx)
		// macOS app bundle: clicking a completion notification brings the
		// window forward, like a second launch does.
		notify.OnClick(func() { showWindow(ctx) })
		// macOS: recordings dragged from Voice Memos arrive as file promises.
		// They are saved in ~/Documents/Nalmeok/<voice memos> (the transcript
		// goes next to them) and then take the normal drop path (OnFileDrop
		// in internal/app). No-op elsewhere.
		if dropDir, err := macdrop.Dir(); err == nil {
			macdrop.Install(dropDir, func(paths []string) {
				wailsruntime.EventsEmit(ctx, "wails:file-drop", 0, 0, paths)
			})
		}
	}
	onShutdown := func(ctx context.Context) {
		if lockErr == nil {
			application.Shutdown(ctx)
		}
	}
	instanceID := "nalmeok"
	if dir, err := config.Dir(); err == nil {
		sum := sha256.Sum256([]byte(dir))
		instanceID = "nalmeok-" + hex.EncodeToString(sum[:6])
	}

	// The window opens as it was left: list only, or list and transcript.
	width, minWidth := app.ListWidth, app.ListMinWidth
	if shared.cfg.Pane() {
		width, minWidth = app.PaneWidth, app.PaneMinWidth
	}
	// macOS gets the standard menu bar (Edit shortcuts, ⌘, and ⌘W); Windows
	// keeps none.
	var appMenu *menu.Menu
	if runtime.GOOS == "darwin" {
		appMenu = application.Menu()
	}
	err = wails.Run(&options.App{
		Title:     "Nalmeok",
		Width:     width,
		Height:    680,
		MinWidth:  minWidth,
		MinHeight: app.WindowMinTall,
		Menu:      appMenu,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop: true,
		},
		Mac: &mac.Options{
			TitleBar:   mac.TitleBarHiddenInset(),
			Appearance: appearance,
		},
		Windows: &windows.Options{
			// The native title bar takes the page colour so the top bar reads
			// as one surface. Windows 10 ignores these colours.
			CustomTheme: &windows.ThemeSettings{
				LightModeTitleBar:          windows.RGB(0xff, 0xff, 0xff),
				LightModeTitleBarInactive:  windows.RGB(0xff, 0xff, 0xff),
				LightModeTitleText:         windows.RGB(0x1d, 0x1d, 0x1f),
				LightModeTitleTextInactive: windows.RGB(0x86, 0x86, 0x8b),
				LightModeBorder:            windows.RGB(0xff, 0xff, 0xff),
				LightModeBorderInactive:    windows.RGB(0xff, 0xff, 0xff),
				DarkModeTitleBar:           windows.RGB(0x1e, 0x1e, 0x1e),
				DarkModeTitleBarInactive:   windows.RGB(0x1e, 0x1e, 0x1e),
				DarkModeTitleText:          windows.RGB(0xf5, 0xf5, 0xf7),
				DarkModeTitleTextInactive:  windows.RGB(0x98, 0x98, 0x9d),
				DarkModeBorder:             windows.RGB(0x1e, 0x1e, 0x1e),
				DarkModeBorderInactive:     windows.RGB(0x1e, 0x1e, 0x1e),
			},
		},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: instanceID,
			OnSecondInstanceLaunch: func(options.SecondInstanceData) {
				ctx := windowCtx.Load()
				if ctx == nil {
					return
				}
				showWindow(*ctx)
			},
		},
		OnStartup:  onStartup,
		OnDomReady: onDomReady,
		OnShutdown: onShutdown,
		// Asks before quitting mid free transcription (close button, ⌘Q, ⌘W).
		OnBeforeClose: application.BeforeClose,
		Bind:          []interface{}{application},
	})
	if err != nil {
		log.Fatal(err)
	}
}

// showWindow brings the hidden or minimised window back to the front.
func showWindow(ctx context.Context) {
	wailsruntime.WindowUnminimise(ctx)
	wailsruntime.Show(ctx)
	wailsruntime.WindowShow(ctx)
}

// runCLI transcribes the given paths without opening a window. It prints one
// line whenever an item's visible state changes and exits once every added
// item is finished.
func runCLI(args []string) int {
	flags := flag.NewFlagSet("nalmeok", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() { printUsage(flags.Output()) }
	batch := flags.Bool("batch", false, i18n.T("cli.flagBatch"))
	free := flags.Bool("free", false, i18n.T("cli.flagFree"))
	keyID := flags.String("key", "", i18n.T("cli.flagKey"))
	backendFlag := flags.String("backend", "", i18n.T("cli.flagBackend"))
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *batch && *free {
		fmt.Fprintln(os.Stderr, i18n.T("cli.batchAndFree"))
		return 2
	}
	paths := flags.Args()
	if len(paths) == 0 {
		printUsage(os.Stderr)
		return 2
	}
	backend := strings.TrimSpace(*backendFlag)
	if backend == "" {
		backend = os.Getenv(backendEnv)
	}
	if _, err := normalizeBackend(backend); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	folderLock, err := config.Lock()
	if err != nil {
		fmt.Fprintln(os.Stderr, lockMessage(err))
		return 1
	}
	defer folderLock.Close()

	cfg, history, engine, signals, err := newRuntime(backend, true)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer engine.Close()
	defer signals.Close()

	mode := cfg.EffectiveMode()
	if *batch {
		mode = "batch"
	} else if *free {
		mode = "free"
	}

	// Remember what existed before Add so only the new items decide the exit
	// status. Items already in the history keep running independently.
	before := make(map[string]bool)
	for _, item := range history.List("") {
		before[item.ID] = true
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine.Start(ctx)

	if err := engine.Add(paths, mode, strings.TrimSpace(*keyID)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	ids := make([]string, 0, len(paths))
	for _, item := range history.List("") {
		if !before[item.ID] {
			ids = append(ids, item.ID)
		}
	}
	if len(ids) == 0 {
		fmt.Fprintln(os.Stderr, i18n.T("cli.nothingAdded"))
		return 1
	}
	return watch(history, engine, ids, cancel)
}

// watch prints state changes until every item is done, failed, canceled or
// free_exhausted. Ctrl+C cancels running items and returns 130.
func watch(history *store.Store, engine *runner.Runner, ids []string, cancel context.CancelFunc) int {
	seen := make(map[string]string, len(ids))
	hinted := make(map[string]bool, len(ids))

	// lines prints any changed status and reports whether every added item
	// reached a terminal state, plus the exit code for those states.
	lines := func() (bool, int) {
		allTerminal := true
		code := 0
		for _, id := range ids {
			item, ok := history.Get(id)
			if !ok {
				continue
			}
			line := statusLine(item)
			if seen[id] != line {
				seen[id] = line
				fmt.Println(line)
				if item.State == runner.StateFreeExhausted && !hinted[id] {
					hinted[id] = true
					fmt.Println(i18n.T("cli.batchHint"))
				}
			}
			switch item.State {
			case runner.StateDone:
			case runner.StateFailed, runner.StateCanceled, runner.StateFreeExhausted:
				code = 1
			default:
				allTerminal = false
			}
		}
		return allTerminal, code
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		if allTerminal, code := lines(); allTerminal {
			return code
		}
		select {
		case <-sig:
			for _, id := range ids {
				if item, ok := history.Get(id); ok && item.State == runner.StateRunning {
					_ = engine.Cancel(id)
				}
			}
			cancel()
			return 130
		case <-ticker.C:
		}
	}
}

func statusLine(item store.Item) string {
	name := item.Name
	if name == "" {
		name = item.Source
	}

	switch item.State {
	case runner.StateRunning:
		if item.Detail == "" || strings.HasPrefix(item.Detail, i18n.T("status.transcribing")) {
			return i18n.T("cli.transcribing", "name", name, "percent", percent(item.Progress))
		}
		return fmt.Sprintf("%s: %s", name, item.Detail)
	case runner.StateBatchWaiting:
		return i18n.T("cli.waiting", "name", name, "minutes", elapsedMinutes(item))
	case runner.StateFreeExhausted:
		return i18n.T("cli.freeExhausted", "name", name)
	case runner.StateDone:
		return i18n.T("cli.done", "name", name, "output", item.Output)
	case runner.StateFailed:
		if item.Detail == "" {
			return i18n.T("cli.failed", "name", name)
		}
		return i18n.T("cli.failedReason", "name", name, "reason", item.Detail)
	case runner.StateCanceled:
		return i18n.T("cli.canceled", "name", name)
	default:
		if item.Detail == "" {
			return fmt.Sprintf("%s: %s", name, item.State)
		}
		return fmt.Sprintf("%s: %s", name, item.Detail)
	}
}

func percent(progress float64) int {
	p := int(math.Round(progress * 100))
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

func elapsedMinutes(item store.Item) int {
	since := item.CreatedAt
	switch {
	case item.Batch != nil && !item.Batch.SubmittedAt.IsZero():
		since = item.Batch.SubmittedAt
	case !item.UpdatedAt.IsZero():
		since = item.UpdatedAt
	}
	minutes := int(time.Since(since).Minutes())
	if minutes < 0 {
		return 0
	}
	return minutes
}

// lockMessage explains a failed folder lock.
func lockMessage(err error) string {
	if errors.Is(err, config.ErrLocked) {
		return i18n.T("cli.locked")
	}
	return i18n.T("cli.lockFailed", "error", err)
}

func runDoctor() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	dir, err := config.Dir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	free, paid := 0, 0
	for _, key := range cfg.AllKeys() {
		if key.Tier == "free" {
			free++
		} else {
			paid++
		}
	}

	items := 0
	if itemsPath, err := config.ItemsPath(); err == nil {
		if history, err := store.Load(itemsPath); err == nil {
			items = len(history.List(""))
		}
	}

	fmt.Printf("Nalmeok %s\n", version)
	fmt.Println(i18n.T("cli.doctorOS", "os", runtime.GOOS, "arch", runtime.GOARCH))
	fmt.Println(i18n.T("cli.doctorDir", "dir", dir))
	fmt.Println(i18n.T("cli.doctorKeys", "free", free, "paid", paid))
	fmt.Println(i18n.T("cli.doctorItems", "count", items))
	fmt.Println(i18n.T("cli.doctorCodex", "state", codexState()))
	return 0
}

// codexState describes the local Codex login for doctor.
func codexState() string {
	auth, err := codexasr.Find()
	if err != nil {
		return i18n.T("cli.codexMissing")
	}
	if auth.Expired(time.Now()) {
		return i18n.T("cli.codexExpired", "path", auth.Path)
	}
	return i18n.T("cli.codexFound", "path", auth.Path)
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, i18n.T("cli.usageIntro"))
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, i18n.T("cli.usage"))
	fmt.Fprintln(w, i18n.T("cli.usageRun"))
	fmt.Fprintln(w, i18n.T("cli.usageBackend"))
	fmt.Fprintln(w, "  nalmeok --version")
	fmt.Fprintln(w, "  nalmeok doctor")
}
