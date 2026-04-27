package web

import (
	"log"
	"math"
	"sync"
	"time"

	"crystal-disk-info-mp/internal/db"
	"crystal-disk-info-mp/internal/smart"
)

const (
	collectInterval = 30 * time.Second
	bufferSize      = 10
)

type tempSample struct {
	value     float64
	timestamp time.Time
}

type Monitor struct {
	collector  smart.Collector
	database   *db.DB
	mu         sync.RWMutex
	disks      []smart.RawDisk
	lastUpdate time.Time
	buffers    map[string][]tempSample
	stopCh     chan struct{}
}

func NewMonitor(collector smart.Collector, database *db.DB) *Monitor {
	return &Monitor{
		collector: collector,
		database:  database,
		buffers:   make(map[string][]tempSample),
		stopCh:    make(chan struct{}),
	}
}

func (m *Monitor) Start() {
	m.collect()
	ticker := time.NewTicker(collectInterval)
	go func() {
		for {
			select {
			case <-ticker.C:
				m.collect()
			case <-m.stopCh:
				ticker.Stop()
				return
			}
		}
	}()
	log.Printf("monitor started, collecting every %v", collectInterval)
}

func (m *Monitor) Stop() {
	close(m.stopCh)
}

func (m *Monitor) GetDisks() []smart.RawDisk {
	m.mu.RLock()
	defer m.mu.RUnlock()
	disks := make([]smart.RawDisk, len(m.disks))
	copy(disks, m.disks)
	return disks
}

func (m *Monitor) GetLastUpdate() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastUpdate
}

func (m *Monitor) ForceRefresh(force bool, id string, index *int) {
	if id == "" && index == nil {
		m.collect()
		return
	}

	m.mu.Lock()
	target, previous, ok := m.findTargetLocked(id, index)
	if !ok {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	disk, err := m.collector.Read(target, force, previous)
	if err != nil {
		log.Printf("force refresh disk %s error: %v", id, err)
		return
	}

	m.mu.Lock()
	m.replaceDiskLocked(disk)
	m.mu.Unlock()
}

func (m *Monitor) collect() {
	disks, err := m.collector.Scan(false)
	if err != nil {
		log.Printf("monitor scan error: %v", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.disks = smart.MergeByID(m.disks, disks)
	m.lastUpdate = time.Now()

	now := time.Now()
	for i := range m.disks {
		disk := &m.disks[i]
		temp := smart.ExtractTemperature(disk)
		if temp > 0 {
			disk.CurrentTemp = &temp
		}

		if temp <= 0 {
			continue
		}

		id := disk.ID
		m.buffers[id] = append(m.buffers[id], tempSample{value: temp, timestamp: now})

		if len(m.buffers[id]) >= bufferSize {
			m.flushBufferLocked(id, disk.Basic.Model, now)
		}
	}
}

func (m *Monitor) flushBufferLocked(diskID, diskModel string, now time.Time) {
	samples := m.buffers[diskID]
	if len(samples) == 0 {
		return
	}

	var sum, minTemp, maxTemp float64
	minTemp = math.MaxFloat64
	maxTemp = -math.MaxFloat64

	for _, s := range samples {
		sum += s.value
		if s.value < minTemp {
			minTemp = s.value
		}
		if s.value > maxTemp {
			maxTemp = s.value
		}
	}
	avgTemp := sum / float64(len(samples))

	if err := m.database.InsertTempRecord(diskID, diskModel, maxTemp, avgTemp, minTemp, len(samples), now); err != nil {
		log.Printf("insert temp record for %s: %v", diskID, err)
	}

	m.buffers[diskID] = nil
}

func (m *Monitor) CurrentTemps() []CurrentTempInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []CurrentTempInfo
	for _, disk := range m.disks {
		info := CurrentTempInfo{
			ID:    disk.ID,
			Model: disk.Basic.Model,
		}
		if disk.CurrentTemp != nil {
			info.CurrentTemp = *disk.CurrentTemp
			info.HasTemp = true
		}
		info.LastUpdated = disk.LastSmartAt
		result = append(result, info)
	}
	return result
}

func (m *Monitor) findTargetLocked(id string, index *int) (int, *smart.RawDisk, bool) {
	for i := range m.disks {
		if (id != "" && m.disks[i].ID == id) || (index != nil && m.disks[i].Index == *index) {
			prev := m.disks[i]
			return m.disks[i].Index, &prev, true
		}
	}
	if index != nil {
		return *index, nil, true
	}
	return 0, nil, false
}

func (m *Monitor) replaceDiskLocked(disk smart.RawDisk) {
	for i := range m.disks {
		if m.disks[i].ID == disk.ID || m.disks[i].Index == disk.Index {
			m.disks[i] = disk
			return
		}
	}
	m.disks = append(m.disks, disk)
}

type CurrentTempInfo struct {
	ID           string  `json:"id"`
	Model        string  `json:"model"`
	CurrentTemp  float64 `json:"currentTemp"`
	HasTemp      bool    `json:"hasTemp"`
	LastUpdated  string  `json:"lastUpdated,omitempty"`
}
