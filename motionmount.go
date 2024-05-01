package motionmount

import (
	"bytes"
	"context"
	"encoding/binary"
	"log/slog"
	"slices"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"tinygo.org/x/bluetooth"
)

// Client is a client for finding and interacting with Vogels MotionMount
// devices over Bluetooth
type Client struct {
	mu      sync.Mutex
	adapter *bluetooth.Adapter
	devices map[bluetooth.Address]*MotionMount
	seen    map[bluetooth.Address]struct{}
}

// DefaultClient returns a client using the default bluetooth adapter
func DefaultClient() (*Client, error) {
	if err := bluetooth.DefaultAdapter.Enable(); err != nil {
		return nil, err
	}

	client := &Client{adapter: bluetooth.DefaultAdapter, devices: make(map[bluetooth.Address]*MotionMount), seen: make(map[bluetooth.Address]struct{})}
	bluetooth.DefaultAdapter.SetConnectHandler(client.connectionHandler)
	return client, nil
}

func (c *Client) connectionHandler(device bluetooth.Device, connected bool) {
	// Log the connection status
	if connected {
		slog.Info("Device is connected", "address", device.Address)
	} else {
		slog.Info("Device is disconnected", "address", device.Address)
	}

	// Update the connected bool on the motionmount object
	if device, exists := c.devices[device.Address]; exists {
		device.mu.Lock()
		defer device.mu.Unlock()
		device.connected = connected
	}
}

// ScanOne will scan for a single MotionMount device and then stop scanning.
// If called multiple times it will not return the same MotionMount device.
func (c *Client) ScanOne(ctx context.Context) (device *MotionMount, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Create output channel for scan
	results := make(chan *MotionMount)

	// When we get a result, cancel the context
	go func() {
		select {
		case <-ctx.Done():
		case device = <-results:
			cancel()
		}
	}()

	// Scan for device
	err = c.Scan(ctx, results)
	return
}

// Scan will scan for MotionMount devices, it will scan continuously until
// the context is canceled adding each new device to the output channel.
//
// It will not return the same MotionMount device multiple times.
func (c *Client) Scan(ctx context.Context, results chan<- *MotionMount) error {
	// Create error group for running tasks
	group, ctx := errgroup.WithContext(ctx)

	// Scan for devices
	group.Go(func() error {
		slog.Info("Scanning for bluetooth devices")
		return c.adapter.Scan(func(a *bluetooth.Adapter, sr bluetooth.ScanResult) {
			// Check the manufacturer data to check if it's a Vogels device, we
			// do this because no service data seems to be included so we need
			// to connect to discover it.
			if !slices.ContainsFunc(sr.AdvertisementPayload.ManufacturerData(), func(manufacturerData bluetooth.ManufacturerDataElement) bool {
				return manufacturerData.CompanyID == vogelsCompanyID
			}) {
				return
			}

			// If the device already exists, just update the lastScan time
			c.mu.Lock()
			if device, exists := c.devices[sr.Address]; exists {
				slog.Info("Scan found known device", "address", sr.Address)
				device.lastScanned = time.Now()
				c.mu.Unlock()
				return
			}
			c.mu.Unlock()

			// This is a new device, we should connect to it and see if it is
			// a motionmount
			go func(sr bluetooth.ScanResult) {
				c.mu.Lock()

				// If device has previously been seen, don't re-evaluate
				if _, exists := c.seen[sr.Address]; exists {
					c.mu.Unlock()
					return
				}

				c.seen[sr.Address] = struct{}{}
				c.mu.Unlock()

				slog.Info("Connecting to device", "address", sr.Address, "name", sr.AdvertisementPayload.LocalName())

				// Connect to the device
				device, err := a.Connect(sr.Address, bluetooth.ConnectionParams{})
				if err != nil {
					// If there is a connection error, remove from the seen
					// list so we re-evaluate it
					c.mu.Lock()
					delete(c.seen, sr.Address)
					c.mu.Unlock()
					return
				}

				// Check for the motion mount service
				if _, err := device.DiscoverServices([]bluetooth.UUID{motionMountServiceUUID}); err != nil {
					slog.Info("Device does not implement MotionMount service", "address", sr.Address)
					device.Disconnect()
					return
				}

				// Obtain the lock so we can create the MotionMount
				c.mu.Lock()
				defer c.mu.Unlock()

				// One final check that the device is not known
				if _, exists := c.devices[sr.Address]; exists {
					return
				}

				slog.Info("Device implements MotionMount service", "address", sr.Address, "name", sr.AdvertisementPayload.LocalName())

				// Create the MotionMount and send it down the results channel
				motionmount := &MotionMount{
					name:        sr.AdvertisementPayload.LocalName(),
					device:      device,
					connected:   true,
					adapter:     a,
					lastScanned: time.Now(),
				}

				c.devices[device.Address] = motionmount

				// If the context is closed, don't block sending it down the
				// channel
				select {
				case <-ctx.Done():
				case results <- motionmount:
				}

			}(sr)
		})
	})

	// Ensure scan is stopped when context is canceled
	group.Go(func() error {
		<-ctx.Done()
		slog.Info("Stopping scan for bluetooth devices")
		return c.adapter.StopScan()
	})

	// Return the results channel
	return group.Wait()
}

// MotionMount is a Vogels MotionMount device
type MotionMount struct {
	mu          sync.Mutex
	name        string
	device      bluetooth.Device
	adapter     *bluetooth.Adapter
	connected   bool
	lastScanned time.Time
}

// Position is a position struct for the MotionMount
type Position struct {
	// Label is defined for "Preset" positions
	Label string
	// WallDistance is an int between 0 and 100.
	WallDistance int16
	// Orientation is an int between -100 and 100, a negative int orients the
	// MotionMount right, a positive int orients it left.
	Orientation int16
}

// Name is the name of the MotionMount device
func (m *MotionMount) Name() string {
	return m.name
}

// Close will disconnect from the MotionMount, the MotionMount will be
// re-connected to for any further calls.
func (m *MotionMount) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.connected {
		m.connected = false
		return m.device.Disconnect()
	}

	return nil
}

// MoveToPosition moves the MotionMount to a given position
func (m *MotionMount) MoveToPosition(position Position) error {
	slog.Info("Moving motion mount to position", "address", m.device.Address, "name", m.name, "preset", position.Label, "wall_distance", position.WallDistance, "orientation", position.Orientation)

	// Discover motionmount service
	service, err := m.getMotionMountService()
	if err != nil {
		return err
	}

	// Load the characteristic used to move
	characteristics, err := service.DiscoverCharacteristics([]bluetooth.UUID{setPositionCharacteristicUUID})
	if err != nil {
		return err
	}

	// Write the position
	var data [4]byte
	binary.BigEndian.PutUint16(data[0:2], uint16(position.WallDistance))
	binary.BigEndian.PutUint16(data[2:4], uint16(position.Orientation))
	if _, err := characteristics[0].Write(data[:]); err != nil {
		return err
	}

	return nil
}

// GetPosition returns the current Position of the MotionMount
func (m *MotionMount) GetPosition() (Position, error) {
	slog.Info("Getting current position", "address", m.device.Address, "name", m.name)

	// Discover motionmount service
	service, err := m.getMotionMountService()
	if err != nil {
		return Position{}, err
	}

	// First we load the characteristics that contain preset data
	characteristics, err := service.DiscoverCharacteristics([]bluetooth.UUID{wallDistanceCharacteristic, orientationCharacteristic})
	if err != nil {
		return Position{}, err
	}

	// Read the characteristics into a buffer
	var buff [4]byte
	characteristics[0].Read(buff[:2])
	characteristics[1].Read(buff[2:4])

	// Convert to position object
	position := Position{
		WallDistance: int16(binary.BigEndian.Uint16(buff[0:2])),
		Orientation:  int16(binary.BigEndian.Uint16(buff[2:4])),
	}

	slog.Info("Get current position", "address", m.device.Address, "name", m.name, "wall_distance", position.WallDistance, "orientation", position.Orientation)

	// Return the position
	return position, nil
}

// GetPositionsPresets returns all configured presets for the MotionMount
func (m *MotionMount) GetPositionsPresets() ([]Position, error) {
	slog.Info("Discovering preset positions", "address", m.device.Address, "name", m.name)

	// Discover motionmount service
	service, err := m.getMotionMountService()
	if err != nil {
		return nil, err
	}

	// First we load the characteristics that contain preset data
	characteristics, err := service.DiscoverCharacteristics(positionCharacteristicUUIDs)
	if err != nil {
		return nil, err
	}

	// There are 7 possible presets, each preset is constructed from two
	// characteristics to allow for longer names.
	//
	// This code reads both characteristics and merges them together to
	// produce the raw preset binary.
	//
	// This binary is then parsed into a "Position" object
	positions := []Position{
		// The "wall" preset is hard-coded and not returned from the motionmount
		{
			Label: "Wall",
		},
	}

	for i := 0; i < 7; i++ {
		// The read calls don't return io.EOF when done, so we cant use io.Copy
		// into a bytes.Buffer that grows. For our use case using a large
		// buffer is probably fine
		buff := [1024]byte{}
		l1, _ := characteristics[i].Read(buff[:])
		l2, _ := characteristics[i+7].Read(buff[l1:])

		// Skip unused presets ([0] contains the enabled bit)
		if buff[0] != 1 {
			continue
		}

		// Get the Position information from the binary data
		position := Position{
			Label:        string(bytes.TrimRight(buff[5:l1+l2], "\x00")),
			WallDistance: int16(binary.BigEndian.Uint16(buff[1:3])),
			Orientation:  int16(binary.BigEndian.Uint16(buff[3:5])),
		}

		slog.Info("Discovered preset position", "address", m.device.Address, "name", m.name, "preset", position.Label, "wall_distance", position.WallDistance, "orientation", position.Orientation)

		positions = append(positions, position)
	}

	return positions, nil
}

func (m *MotionMount) ensureConnection() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.connected {
		slog.Info("Reconnecting to motion mount", "address", m.device.Address, "name", m.name)
		device, err := m.adapter.Connect(m.device.Address, bluetooth.ConnectionParams{})
		if err != nil {
			return err
		}

		m.connected = true
		m.device = device
	}

	return nil
}

func (m *MotionMount) getMotionMountService() (bluetooth.DeviceService, error) {
	// Ensure we have a connection to the device
	if err := m.ensureConnection(); err != nil {
		return bluetooth.DeviceService{}, err
	}

	// Discover motionmount service
	services, err := m.device.DiscoverServices([]bluetooth.UUID{motionMountServiceUUID})
	if err != nil {
		return bluetooth.DeviceService{}, err
	}

	// Return the service
	return services[0], nil
}
