package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/JonathanBencke/ServiceManagerTUI/internal/config"
	"github.com/JonathanBencke/ServiceManagerTUI/internal/kiro"
	"github.com/JonathanBencke/ServiceManagerTUI/internal/mcp"
	"github.com/JonathanBencke/ServiceManagerTUI/internal/service"
	"github.com/JonathanBencke/ServiceManagerTUI/internal/tui"
	"github.com/JonathanBencke/ServiceManagerTUI/internal/webconfig"
	tea "github.com/charmbracelet/bubbletea"
)

var (
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	user32               = syscall.NewLazyDLL("user32.dll")
	procSetConsoleTitle  = kernel32.NewProc("SetConsoleTitleW")
	procGetConsoleWindow = kernel32.NewProc("GetConsoleWindow")
	procLoadIconW        = user32.NewProc("LoadIconW")
	procSendMessageW     = user32.NewProc("SendMessageW")
	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
)

const (
	WM_SETICON = 0x0080
	ICON_BIG   = 1
	ICON_SMALL = 0
)

func setConsoleTitle(title string) {
	ptr, _ := syscall.UTF16PtrFromString(title)
	procSetConsoleTitle.Call(uintptr(unsafe.Pointer(ptr)))
}

func setConsoleIcon() {
	hwnd, _, _ := procGetConsoleWindow.Call()
	if hwnd == 0 {
		return
	}

	hModule, _, _ := procGetModuleHandleW.Call(0)
	hIcon, _, _ := procLoadIconW.Call(hModule, 1)
	if hIcon == 0 {
		return
	}

	procSendMessageW.Call(hwnd, WM_SETICON, ICON_BIG, hIcon)
	procSendMessageW.Call(hwnd, WM_SETICON, ICON_SMALL, hIcon)
}

func main() {
	mcpMode := flag.Bool("mcp", false, "Run as MCP server (for Claude Code / kiro-cli integration)")
	installMCP := flag.Bool("install-mcp", false, "Register this app as an MCP server in kiro-cli, then exit")
	uninstallMCP := flag.Bool("uninstall-mcp", false, "Remove this app from kiro-cli's MCP servers, then exit")
	scope := flag.String("scope", "global", "Scope for -install-mcp/-uninstall-mcp: global or workspace")
	flag.Parse()

	if *installMCP {
		runInstallMCP(*scope)
		return
	}
	if *uninstallMCP {
		runUninstallMCP(*scope)
		return
	}

	cfgPath := config.DefaultPath()
	if flag.NArg() > 0 {
		cfgPath = flag.Arg(0)
	}

	if *mcpMode {
		runMCP(cfgPath)
		return
	}

	runTUI(cfgPath)
}

// runInstallMCP registers the TUI's running SSE server as an MCP server in
// kiro-cli's settings, so the assistant connects to the same server (and shared
// service state) that the TUI starts — no separate process is spawned.
func runInstallMCP(scope string) {
	url := fmt.Sprintf("http://127.0.0.1:%d/mcp", mcp.DefaultPort)

	path, err := kiro.InstallMCP(scope, url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error registering MCP server: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Registered MCP server %q (%s) in %s\n", kiro.DefaultServerName, url, path)
	fmt.Println("Keep the Service Manager TUI open (MCP is on by default) so kiro-cli can connect.")
	fmt.Println("Restart kiro-cli (or run '/mcp' in chat) to pick it up.")
}

// runUninstallMCP removes the MCP server entry from kiro-cli's settings.
func runUninstallMCP(scope string) {
	path, removed, err := kiro.UninstallMCP(scope)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error removing MCP server: %v\n", err)
		os.Exit(1)
	}
	if removed {
		fmt.Printf("Removed MCP server %q from %s\n", kiro.DefaultServerName, path)
	} else {
		fmt.Printf("MCP server %q was not present in %s\n", kiro.DefaultServerName, path)
	}
}

func runMCP(cfgPath string) {
	srv, err := mcp.NewServer(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating MCP server: %v\n", err)
		os.Exit(1)
	}

	if err := srv.Serve(); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}

// startupInfo carries the resolved configuration and whether the TUI should
// open on the onboarding screen (first run or no services configured yet).
type startupInfo struct {
	cfg        *config.Config
	onboarding bool
}

// resolveStartup prepares the configuration for the TUI. If the config file
// does not exist it generates a commented starter services.toml. It then loads
// the configuration tolerating an empty service list, flagging onboarding when
// no services are defined yet so the caller can show the welcome screen instead
// of crashing.
func resolveStartup(cfgPath string) (startupInfo, error) {
	if !config.Exists(cfgPath) {
		if err := config.WriteStarter(cfgPath); err != nil {
			return startupInfo{}, fmt.Errorf("creating starter config: %w", err)
		}
	}

	cfg, err := config.LoadAllowEmpty(cfgPath)
	if err != nil {
		return startupInfo{}, err
	}

	return startupInfo{cfg: cfg, onboarding: len(cfg.Services) == 0}, nil
}

func runTUI(cfgPath string) {
	startup, err := resolveStartup(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	manager := service.NewManager(startup.cfg)
	model := tui.NewModel(manager)
	model.SetConfigPath(cfgPath)
	if startup.onboarding {
		model.StartOnboarding()
	}

	mcpSrv := mcp.NewServerFromManager(manager)
	mcpSrv.SetConfigPath(cfgPath)
	model.SetMCPServer(mcpSrv)

	webSrv := webconfig.New(cfgPath)
	model.SetWebServer(webSrv)

	setConsoleTitle("Service Manager")
	setConsoleIcon()

	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())

	// Throttle de render: logs chegam em rajada (Java/Maven/WildFly verbosos),
	// entao em vez de enviar uma msg por linha (centenas de renders/seg), apenas
	// sinalizamos "dirty" e um ticker envia no maximo um sinal de render a cada
	// 80ms. As linhas seguem indo para o ring buffer do servico; o render le de la.
	//
	// Os mesmos eventos alimentam o servidor MCP, que invalida os resources
	// correspondentes (com debounce proprio) para clientes inscritos — sem isso,
	// um agente so descobriria mudancas fazendo polling de list_services.
	var logDirty int32
	onLog := func(name, line string) {
		atomic.StoreInt32(&logDirty, 1)
		mcpSrv.NotifyServiceLog(name, line)
	}
	onStatus := func(name string, status service.Status, pid int) {
		p.Send(tui.StatusMsg(name, status, pid))
		mcpSrv.NotifyServiceStatus(name, status, pid)
	}
	onMCPLog := func(line string) {
		atomic.StoreInt32(&logDirty, 1)
	}

	manager.SetCallbacks(onLog, onStatus)
	model.SetCallbacks(onLog, onStatus)
	mcpSrv.SetLogCallback(onMCPLog)

	// A pagina de configuracao, ao salvar, pede reload do TUI. Seus logs vao
	// para o mesmo mecanismo de render dirty.
	webSrv.OnSaved = func() { p.Send(tui.ReloadRequestMsg()) }
	webSrv.SetLogCallback(onMCPLog)

	// Habilita o servidor MCP por padrao ao abrir o TUI.
	if err := mcpSrv.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not start MCP server: %v\n", err)
	}

	tickerCtx, cancelTicker := context.WithCancel(context.Background())
	go func() {
		t := time.NewTicker(80 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-tickerCtx.Done():
				return
			case <-t.C:
				if atomic.SwapInt32(&logDirty, 0) == 1 {
					p.Send(tui.LogMsg("", ""))
				}
			}
		}
	}()

	if _, err := p.Run(); err != nil {
		cancelTicker()
		if mcpSrv.IsRunning() {
			mcpSrv.Stop()
		}
		webSrv.Stop()
		if manager.RunningCount() > 0 {
			manager.StopAllSync(15 * time.Second)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	cancelTicker()

	if manager.RunningCount() > 0 {
		setConsoleTitle("Service Manager - stopping services...")
		manager.StopAllSync(15 * time.Second)
	}

	if mcpSrv.IsRunning() {
		mcpSrv.Stop()
	}
	webSrv.Stop()
}
