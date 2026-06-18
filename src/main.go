package main

//go:generate go run github.com/go-bindata/go-bindata/go-bindata -pkg $GOPACKAGE -o assets.go assets/

import (
	"bufio"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"os/user"
	"sync"
	"syscall"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"github.com/apenwarr/fixconsole"
	"github.com/getlantern/systray"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

var (
	unixSocket     = flag.String("wsl", "", "Path to Unix socket for passthrough to WSL")
	namedPipe      = flag.String("winssh", "", "Named pipe for use with Win32 OpenSSH")
	verbose        = flag.Bool("verbose", false, "Enable verbose logging")
	systrayFlag    = flag.Bool("systray", false, "Enable systray integration")
	force          = flag.Bool("force", false, "Force socket usage (unlink existing socket)")
	maxConnections = flag.Int("max-connections", 16, "Maximum number of concurrent connections per listener")
)

const (
	// Windows constants
	invalidHandleValue = ^windows.Handle(0)
	pageReadWrite      = 0x4
	fileMapWrite       = 0x2

	// ssh-agent/Pageant constants
	agentMaxMessageLength = 8192
	agentCopyDataID       = 0x804e50ba

)

// copyDataStruct is used to pass data in the WM_COPYDATA message.
// copyDataStruct matches the Windows COPYDATASTRUCT layout exactly.
// lpData is unsafe.Pointer (not uintptr) so the GC treats it as a live
// reference and does not collect the pointed-to object before SendMessage returns.
type copyDataStruct struct {
	dwData uintptr
	cbData uint32
	lpData unsafe.Pointer
}

type SecurityAttributes struct {
	Length             uint32
	SecurityDescriptor uintptr
	InheritHandle      uint32
}

var queryPageantMutex sync.Mutex

func currentUserSID() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.Uid
}

func makeInheritSaWithSid() *windows.SecurityAttributes {
	var sa windows.SecurityAttributes

	// "O:<sid>D:P(A;;GA;;;<sid>)" sets owner and an explicit DACL granting
	// full access only to the current user. Without the D: component the
	// descriptor would have a NULL DACL (world-readable).
	sid := currentUserSID()
	if sid != "" {
		sd, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;;GA;;;" + sid + ")")
		if err == nil {
			sa.SecurityDescriptor = sd
		}
	}

	sa.Length = uint32(unsafe.Sizeof(sa))
	sa.InheritHandle = 1

	return &sa
}

func queryPageant(buf []byte) (result []byte, err error) {
	if len(buf) > agentMaxMessageLength {
		err = errors.New("Message too long")
		return
	}

	pageantClass, _ := windows.UTF16PtrFromString("Pageant")
	pageantTitle, _ := windows.UTF16PtrFromString("Pageant")
	hwnd := win.FindWindow(pageantClass, pageantTitle)
	if hwnd == 0 {
		err = errors.New("Could not find Pageant window")
		return
	}

	// Calls to queryPageant are serialised because Go provides no way to
	// retrieve the current goroutine ID, which would otherwise be embedded
	// in the shared memory map name to allow concurrent access.
	mapName := "WSLPageantRequest"
	queryPageantMutex.Lock()

	var sa = makeInheritSaWithSid()

	mapNamePtr, _ := windows.UTF16PtrFromString(mapName)
	fileMap, err := windows.CreateFileMapping(invalidHandleValue, sa, pageReadWrite, 0, agentMaxMessageLength, mapNamePtr)
	if err != nil {
		queryPageantMutex.Unlock()
		return
	}
	defer func() {
		windows.CloseHandle(fileMap)
		queryPageantMutex.Unlock()
	}()

	sharedMemory, err := windows.MapViewOfFile(fileMap, fileMapWrite, 0, 0, 0)
	if err != nil {
		return
	}
	defer windows.UnmapViewOfFile(sharedMemory)

	sharedMemoryArray := (*[agentMaxMessageLength]byte)(unsafe.Pointer(sharedMemory))
	copy(sharedMemoryArray[:], buf)

	mapNameWithNul := mapName + "\000"
	cds := copyDataStruct{
		dwData: agentCopyDataID,
		cbData: uint32(len(mapNameWithNul)),
		lpData: unsafe.Pointer(unsafe.StringData(mapNameWithNul)),
	}

	ret := win.SendMessage(hwnd, win.WM_COPYDATA, 0, uintptr(unsafe.Pointer(&cds)))
	if ret == 0 {
		err = errors.New("WM_COPYDATA failed")
		return
	}

	msgLen := binary.BigEndian.Uint32(sharedMemoryArray[:4])
	// Validate before adding 4 to prevent uint32 overflow wrapping past the check.
	if msgLen > agentMaxMessageLength-4 {
		err = errors.New("Return message too long")
		return
	}
	msgLen += 4

	result = make([]byte, msgLen)
	copy(result, sharedMemoryArray[:msgLen])

	return
}

var failureMessage = [...]byte{0, 0, 0, 1, 5}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)

	for {
		lenBuf := make([]byte, 4)
		_, err := io.ReadFull(reader, lenBuf)
		if err != nil {
			if *verbose {
				log.Printf("io.ReadFull error '%s'", err)
			}
			return
		}

		msgLen := binary.BigEndian.Uint32(lenBuf)
		if msgLen > agentMaxMessageLength {
			if *verbose {
				log.Printf("Client sent oversized message: %d bytes", msgLen)
			}
			return
		}
		buf := make([]byte, msgLen)
		_, err = io.ReadFull(reader, buf)
		if err != nil {
			if *verbose {
				log.Printf("io.ReadFull error '%s'", err)
			}
			return
		}

		result, err := queryPageant(append(lenBuf, buf...))
		if err != nil {
			// If for some reason talking to Pageant fails we fall back to
			// sending an agent error to the client
			if *verbose {
				log.Printf("Pageant query error '%s'", err)
			}
			result = failureMessage[:]
		}

		_, err = conn.Write(result)
		if err != nil {
			if *verbose {
				log.Printf("net.Conn.Write error '%s'", err)
			}
			return
		}
	}
}

func listenLoop(ln net.Listener) {
	defer ln.Close()

	sem := make(chan struct{}, *maxConnections)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("net.Listener.Accept error '%s'", err)
			return
		}

		select {
		case sem <- struct{}{}:
		default:
			log.Printf("Connection limit reached (%d), dropping connection", *maxConnections)
			conn.Close()
			continue
		}

		if *verbose {
			log.Printf("New connection: %v\n", conn)
		}

		go func() {
			defer func() { <-sem }()
			handleConnection(conn)
		}()
	}
}

// openListeners initializes unix socket and/or named pipe listeners based on flags.
func openListeners() (unix net.Listener, pipe net.Listener, err error) {
	if *unixSocket != "" {
		_, statErr := os.Stat(*unixSocket)
		if statErr == nil || !os.IsNotExist(statErr) {
			if *force {
				if unlinkErr := syscall.Unlink(*unixSocket); unlinkErr != nil {
					err = fmt.Errorf("failed to unlink socket %s: %w", *unixSocket, unlinkErr)
					return
				}
			} else {
				err = fmt.Errorf("SSH_AUTH_SOCK file already exists; use --force to remove it")
				return
			}
		}

		unix, err = net.Listen("unix", *unixSocket)
		if err != nil {
			err = fmt.Errorf("could not open socket %s: %w", *unixSocket, err)
			return
		}
		log.Printf("Listening on Unix socket: %s", *unixSocket)
	}

	if *namedPipe != "" {
		namedPipeFullName := `\\.\pipe\` + *namedPipe
		pipeCfg := winio.PipeConfig{}
		if sid := currentUserSID(); sid != "" {
			pipeCfg.SecurityDescriptor = "D:P(A;;GA;;;" + sid + ")"
		}
		pipe, err = winio.ListenPipe(namedPipeFullName, &pipeCfg)
		if err != nil {
			if unix != nil {
				unix.Close()
			}
			err = fmt.Errorf("could not open named pipe %s: %w", namedPipeFullName, err)
			return
		}
		log.Printf("Listening on named pipe: %s", namedPipeFullName)
	}

	if unix == nil && pipe == nil {
		err = fmt.Errorf("no socket or pipe specified")
	}
	return
}

// startListeners launches goroutines for each listener and signals done if either exits.
// The done channel must have capacity >= number of active listeners.
func startListeners(done chan bool, unix, pipe net.Listener) {
	if unix != nil {
		go func() {
			listenLoop(unix)
			done <- true
		}()
	}
	if pipe != nil {
		go func() {
			listenLoop(pipe)
			done <- true
		}()
	}
}

func main() {
	fixconsole.FixConsoleIfNeeded()
	flag.Parse()

	unix, pipe, err := openListeners()
	if err != nil {
		flag.PrintDefaults()
		log.Fatalf("Error: %v", err)
	}

	// Buffer of 2: one slot per potential listener (unix + pipe).
	done := make(chan bool, 2)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigs
		switch sig {
		case os.Interrupt, syscall.SIGTERM:
			log.Printf("Caught signal: %v", sig)
			done <- true
		}
	}()

	startListeners(done, unix, pipe)

	if *systrayFlag {
		go func() {
			<-done
			systray.Quit()
		}()

		systray.Run(onSystrayReady, nil)
	} else {
		<-done
	}

	log.Print("Exiting...")
}

func onSystrayReady() {
	systray.SetTitle("WSL-SSH-Pageant")
	systray.SetTooltip("WSL-SSH-Pageant")

	data, err := Asset("assets/icon.ico")
	if err == nil {
		systray.SetIcon(data)
	}

	quit := systray.AddMenuItem("Quit", "Quits this app")

	go func() {
		for range quit.ClickedCh {
			systray.Quit()
			return
		}
	}()
}
