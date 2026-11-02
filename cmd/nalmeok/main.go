package main

import (
	"context"
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
	"strings"
	"time"

	"github.com/jseook11/nalmeok/frontend"
	"github.com/jseook11/nalmeok/internal/app"
	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/gemini"
	"github.com/jseook11/nalmeok/internal/plan"
	"github.com/jseook11/nalmeok/internal/runner"
	"github.com/jseook11/nalmeok/internal/store"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// version is overridden at build time with
// -ldflags "-X main.version=...".
var version = "0.7.0-dev"

func main() {
	args := filterPSNArgs(os.Args[1:])
	if len(args) == 0 {
		runGUI()
		return
	}

	switch args[0] {
	case "--version", "-version":
		fmt.Println(version)
	case "doctor":
		os.Exit(runDoctor())
	default:
		os.Exit(runCLI(args))
	}
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

// newRuntime wires the config, history and runner shared by the GUI and CLI.
func newRuntime() (*config.Config, *store.Store, *runner.Runner, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, nil, err
	}
	itemsPath, err := config.ItemsPath()
	if err != nil {
		return nil, nil, nil, err
	}
	history, err := store.Load(itemsPath)
	if err != nil {
		return nil, nil, nil, err
	}

	workDir, err := config.WorkDir()
	if err != nil {
		return nil, nil, nil, err
	}
	logPath, err := config.LogPath()
	if err != nil {
		return nil, nil, nil, err
	}
	engine := runner.New(runner.Options{
		Store:          history,
		Media:          runner.DefaultMedia{},
		NewClient:      gemini.New,
		WorkDir:        workDir,
		LogPath:        logPath,
		UnitMaxSeconds: plan.UnitMaxSeconds,
	})
	return cfg, history, engine, nil
}

func runGUI() {
	cfg, history, engine, err := newRuntime()
	if err != nil {
		log.Fatal(err)
	}

	var application *app.App
	var onDomReady func(context.Context)
	appearance := mac.DefaultAppearance
	if os.Getenv("NALMEOK_UI_PREVIEW") == "1" {
		// Screenshot mode: the saved items stay as they are and nothing
		// touches the network. See docs/development.md.
		application = app.New(cfg, history, app.NewStubRunner(history))
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
		application = app.New(cfg, history, engine)
		application.ValidateKey = gemini.ValidateKey
	}

	assets, err := fs.Sub(frontend.Assets, "dist")
	if err != nil {
		log.Fatal(err)
	}

	err = wails.Run(&options.App{
		Title:     "날먹",
		Width:     480,
		Height:    680,
		MinWidth:  420,
		MinHeight: 560,
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
		OnStartup:  application.Startup,
		OnDomReady: onDomReady,
		OnShutdown: application.Shutdown,
		Bind:       []interface{}{application},
	})
	if err != nil {
		log.Fatal(err)
	}
}

// runCLI transcribes the given paths without opening a window. It prints one
// line whenever an item's visible state changes and exits once every added
// item is finished.
func runCLI(args []string) int {
	cfg, history, engine, err := newRuntime()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	flags := flag.NewFlagSet("nalmeok", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() { printUsage(flags.Output()) }
	batch := flags.Bool("batch", false, "유료로 처리")
	free := flags.Bool("free", false, "무료로 처리")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *batch && *free {
		fmt.Fprintln(os.Stderr, "--batch와 --free는 함께 쓸 수 없어요")
		return 2
	}
	paths := flags.Args()
	if len(paths) == 0 {
		printUsage(os.Stderr)
		return 2
	}

	mode := cfg.Mode()
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

	if err := engine.Add(paths, mode); err != nil {
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
		fmt.Fprintln(os.Stderr, "추가된 항목이 없어요")
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
					fmt.Println("유료로 이어 가려면 --batch로 다시 실행하세요")
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
		if item.Detail == "" || strings.HasPrefix(item.Detail, "전사 중") {
			return fmt.Sprintf("%s: 전사 중 %d%%", name, percent(item.Progress))
		}
		return fmt.Sprintf("%s: %s", name, item.Detail)
	case runner.StateBatchWaiting:
		return fmt.Sprintf("%s: 기다리는 중 · %d분", name, elapsedMinutes(item))
	case runner.StateFreeExhausted:
		return fmt.Sprintf("%s: 오늘 무료 사용량을 다 썼어요", name)
	case runner.StateDone:
		return fmt.Sprintf("%s: 완료 → %s", name, item.Output)
	case runner.StateFailed:
		if item.Detail == "" {
			return fmt.Sprintf("%s: 실패", name)
		}
		return fmt.Sprintf("%s: 실패 · %s", name, item.Detail)
	case runner.StateCanceled:
		return fmt.Sprintf("%s: 취소됨", name)
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

	fmt.Printf("날먹 %s\n", version)
	fmt.Printf("OS/아키텍처: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("설정 폴더: %s\n", dir)
	fmt.Printf("키: 무료 %d개, 유료 %d개\n", free, paid)
	fmt.Printf("items.json 항목: %d개\n", items)
	return 0
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "테스트·AI 에이전트용 CLI예요. 보통은 인자 없이 실행해 앱 창을 쓰세요.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "사용법:")
	fmt.Fprintln(w, "  nalmeok [--batch|--free] <파일 또는 폴더>...")
	fmt.Fprintln(w, "  nalmeok --version")
	fmt.Fprintln(w, "  nalmeok doctor")
}
