package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
	"tinygo.org/x/bluetooth"
)

var (
	adapter          = bluetooth.DefaultAdapter
	deskAddr         = "CA:9E:91:D9:CB:3E" // Your desk address
	controlCharUUID  = "99fa0002-338a-1024-8a49-009c0215f78a"
	positionCharUUID = "99fa0021-338a-1024-8a49-009c0215f78a"

	cmdUp   = []byte{0x47, 0x00}
	cmdDown = []byte{0x46, 0x00}
	cmdStop = []byte{0xFF, 0x00}

	offsetCm = 62.0
)

type DeskClient struct {
	device *bluetooth.Device
	ctrl   *bluetooth.DeviceCharacteristic
	pos    *bluetooth.DeviceCharacteristic
	mu     sync.Mutex
}

var client = &DeskClient{}
var (
	moveCancel context.CancelFunc
	cancelMu   sync.Mutex
	lastActivity time.Time
	deskProps *prop.Properties
)

func markActivity() {
	client.mu.Lock()
	defer client.mu.Unlock()
	lastActivity = time.Now()
}

func isMoving() bool {
	cancelMu.Lock()
	defer cancelMu.Unlock()
	return moveCancel != nil
}

func main() {
	// Clean up any zombie BlueZ connections from ungraceful logout shutdowns
	log.Printf("Booting up: clearing potential zombie BlueZ connection to %s", deskAddr)
	cmd := exec.Command("bluetoothctl", "disconnect", deskAddr)
	cmd.Run()
	time.Sleep(1 * time.Second)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-c
		cancelOngoingMove()
		
		// Force exit after 1 second if graceful disconnect hangs
		go func() {
			time.Sleep(1 * time.Second)
			os.Exit(0)
		}()

		client.mu.Lock()
		if client.device != nil {
			client.device.Disconnect()
		}
		client.mu.Unlock()
		os.Exit(0)
	}()

	initDBus()
}

type DeskController struct{}

func writeCmd(cmd []byte) error {
	client.mu.Lock()
	ctrl := client.ctrl
	client.mu.Unlock()
	
	if ctrl == nil {
		return fmt.Errorf("not connected")
	}
	
	_, err := ctrl.WriteWithoutResponse(cmd)
	if err != nil {
		log.Printf("Write error: %v", err)
		// Ignore "In Progress" error which occurs when BlueZ receives rapid overlapping commands
		if err.Error() != "In Progress" {
			client.mu.Lock()
			client.disconnect()
			client.mu.Unlock()
		}
	}
	return err
}

func startMoveDirection(ctx context.Context, cmd []byte) {
	log.Printf("startMoveDirection: starting, ensureConnected...")
	err := client.ensureConnected()
	if err != nil {
		log.Printf("Connect error: %v", err)
		return
	}

	// If cancelled while connecting, exit cleanly
	select {
	case <-ctx.Done():
		log.Printf("Move cancelled before starting")
		return
	default:
	}

	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	log.Printf("startMoveDirection: writing initial command")
	markActivity()
	writeCmd(cmd)

	for {
		markActivity()
		select {
		case <-ctx.Done():
			log.Printf("Move cancelled, writing cmdStop")
			writeCmd(cmdStop)
			return
		case <-ticker.C:
			err = writeCmd(cmd)
			if err != nil && err.Error() != "In Progress" {
				log.Printf("Write error during continuous move: %v", err)
				return
			}
		}
	}
}

func (d DeskController) Up() *dbus.Error {
	log.Printf("DBUS Request: Up")
	startMove(func(ctx context.Context) {
		startMoveDirection(ctx, cmdUp)
	})
	return nil
}

func (d DeskController) Down() *dbus.Error {
	log.Printf("DBUS Request: Down")
	startMove(func(ctx context.Context) {
		startMoveDirection(ctx, cmdDown)
	})
	return nil
}

func (d DeskController) Stop() *dbus.Error {
	log.Printf("DBUS Request: Stop")
	cancelOngoingMove()
	go sendCommand(cmdStop)
	return nil
}

func (d DeskController) MoveToSit() *dbus.Error {
	log.Printf("DBUS Request: MoveToSit")
	startMove(func(ctx context.Context) {
		moveTo(ctx, 75.0)
	})
	return nil
}

func (d DeskController) MoveToStand() *dbus.Error {
	log.Printf("DBUS Request: MoveToStand")
	startMove(func(ctx context.Context) {
		moveTo(ctx, 110.0)
	})
	return nil
}

func (d DeskController) RefreshPosition() *dbus.Error {
	log.Printf("DBUS Request: RefreshPosition")
	go func() {
		err := client.ensureConnected()
		if err == nil {
			markActivity()
			height, err := updatePositionNoConnect()
			if err == nil && deskProps != nil {
				log.Printf("RefreshPosition: Setting DBus Position to %.2f", height)
				dbusErr := deskProps.Set("org.linak.Desk", "Position", dbus.MakeVariant(height))
				if dbusErr != nil {
					log.Printf("RefreshPosition: DBus Set Error: %v", dbusErr)
				}
			} else {
				log.Printf("RefreshPosition: Failed to update position: %v", err)
			}
		} else {
			log.Printf("RefreshPosition: ensureConnected failed: %v", err)
		}
	}()
	return nil
}

func initDBus() {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		log.Printf("Failed to connect to session bus: %v", err)
		return
	}
	defer conn.Close()

	reply, err := conn.RequestName("org.linak.Desk", dbus.NameFlagReplaceExisting)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		log.Printf("Failed to request name: %v", err)
		return
	}

	desk := DeskController{}
	conn.Export(desk, "/org/linak/Desk", "org.linak.Desk")

	// Introspection
	node := &introspect.Node{
		Name: "/org/linak/Desk",
		Interfaces: []introspect.Interface{
			{
				Name:    "org.linak.Desk",
				Methods: introspect.Methods(desk),
				Properties: []introspect.Property{
					{
						Name:   "Position",
						Type:   "d",
						Access: "read",
					},
				},
			},
		},
	}
	conn.Export(introspect.NewIntrospectable(node), "/org/linak/Desk", "org.freedesktop.DBus.Introspectable")

	propsSpec := map[string]map[string]*prop.Prop{
		"org.linak.Desk": {
			"Position": {
				Value:    0.0,
				Writable: true,
				Emit:     prop.EmitTrue,
				Callback: func(c *prop.Change) *dbus.Error {
					return nil
				},
			},
		},
	}

	props, err := prop.Export(conn, "/org/linak/Desk", propsSpec)
	if err != nil {
		log.Printf("Failed to export properties: %v", err)
		return
	}
	deskProps = props

	// Update loop for DBus property
	go func() {
		for {
			client.mu.Lock()
			connected := client.device != nil
			client.mu.Unlock()

			if connected {
				height, err := updatePositionNoConnect()
				if err == nil && deskProps != nil {
					deskProps.Set("org.linak.Desk", "Position", dbus.MakeVariant(height))
				} else if err != nil {
					log.Printf("Background loop update position error: %v", err)
				}
			}
			time.Sleep(5 * time.Second)
		}
	}()

	// Idle disconnect loop
	go func() {
		for {
			time.Sleep(5 * time.Second)
			client.mu.Lock()
			shouldDisconnect := client.device != nil && time.Since(lastActivity) > 15*time.Second
			client.mu.Unlock()

			if shouldDisconnect && !isMoving() {
				log.Println("Idle timeout reached, disconnecting bluetooth to free up device")
				client.mu.Lock()
				client.disconnect()
				client.mu.Unlock()
			}
		}
	}()

	select {}
}

func cancelOngoingMove() {
	cancelMu.Lock()
	defer cancelMu.Unlock()
	if moveCancel != nil {
		moveCancel()
		moveCancel = nil
	}
}

func startMove(f func(ctx context.Context)) {
	log.Printf("startMove: cancelling ongoing move")
	cancelOngoingMove()
	
	cancelMu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	moveCancel = cancel
	cancelMu.Unlock()
	
	go f(ctx)
}



func (c *DeskClient) ensureConnected() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.device != nil {
		return nil
	}

	err := adapter.Enable()
	if err != nil {
		return err
	}

	mac, err := bluetooth.ParseMAC(deskAddr)
	if err != nil {
		return err
	}
	addr := bluetooth.Address{MACAddress: bluetooth.MACAddress{MAC: mac}}

	log.Printf("Connecting to %s...", deskAddr)
	device, err := adapter.Connect(addr, bluetooth.ConnectionParams{})
	if err != nil {
		return err
	}
	lastActivity = time.Now()
	c.device = &device

	svcs, err := device.DiscoverServices(nil)
	if err != nil {
		c.disconnect()
		return err
	}

	var ctrlChar, posChar *bluetooth.DeviceCharacteristic
	for _, svc := range svcs {
		chars, _ := svc.DiscoverCharacteristics(nil)
		for _, char := range chars {
			if char.UUID().String() == controlCharUUID {
				charCopy := char
				ctrlChar = &charCopy
			} else if char.UUID().String() == positionCharUUID {
				charCopy := char
				posChar = &charCopy
			}
		}
	}

	if ctrlChar == nil || posChar == nil {
		c.disconnect()
		return fmt.Errorf("required characteristics not found")
	}

	c.ctrl = ctrlChar
	c.pos = posChar
	log.Printf("Connected successfully")
	return nil
}

func (c *DeskClient) disconnect() {
	if c.device != nil {
		c.device.Disconnect()
		c.device = nil
		c.ctrl = nil
		c.pos = nil
	}
}

func sendCommand(cmd []byte) {
	err := client.ensureConnected()
	if err != nil {
		log.Printf("Connect error: %v", err)
		return
	}
	markActivity()
	writeCmd(cmd)
}

func updatePosition() (float64, error) {
	err := client.ensureConnected()
	if err != nil {
		return 0, err
	}

	buf := make([]byte, 2)
	_, err = client.pos.Read(buf)
	if err != nil {
		log.Printf("Read error: %v", err)
		client.mu.Lock()
		client.disconnect()
		client.mu.Unlock()
		return 0, err
	}

	raw := binary.LittleEndian.Uint16(buf)
	return (float64(raw) / 100.0) + offsetCm, nil
}

func updatePositionNoConnect() (float64, error) {
	client.mu.Lock()
	if client.device == nil || client.pos == nil {
		client.mu.Unlock()
		return 0, fmt.Errorf("not connected")
	}
	posChar := client.pos
	client.mu.Unlock()

	buf := make([]byte, 2)
	_, err := posChar.Read(buf)
	if err != nil {
		log.Printf("posChar.Read error: %v", err)
		return 0, err
	}

	raw := binary.LittleEndian.Uint16(buf)
	height := (float64(raw) / 100.0) + offsetCm
	log.Printf("Read raw position: %v, height: %.2f", buf, height)
	return height, nil
}

func moveTo(ctx context.Context, target float64) {
	err := client.ensureConnected()
	if err != nil {
		log.Printf("Connect error: %v", err)
		return
	}

	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	for {
		markActivity()
		select {
		case <-ctx.Done():
			log.Printf("Move to %.1f cancelled", target)
			writeCmd(cmdStop)
			return
		case <-ticker.C:
			client.mu.Lock()
			if client.device == nil || client.pos == nil {
				client.mu.Unlock()
				return
			}
			posChar := client.pos
			client.mu.Unlock()
			
			buf := make([]byte, 2)
			_, err = posChar.Read(buf)
			if err != nil {
				log.Printf("Read error during moveTo: %v", err)
				client.mu.Lock()
				client.disconnect()
				client.mu.Unlock()
				return
			}
			raw := binary.LittleEndian.Uint16(buf)
			current := (float64(raw) / 100.0) + offsetCm

			diff := target - current
			if diff > -0.5 && diff < 0.5 {
				writeCmd(cmdStop)
				return
			}

			cmd := cmdUp
			if diff < 0 {
				cmd = cmdDown
			}

			err = writeCmd(cmd)
			if err != nil && err.Error() != "In Progress" {
				log.Printf("Write error during moveTo: %v", err)
				return
			}
		}
	}
}


