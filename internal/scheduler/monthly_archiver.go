package scheduler

import (
	"log"
	"time"

	"github.com/DullJZ/s3-balance/internal/storage"
)

// MonthlyArchiver 月度统计归档器
type MonthlyArchiver struct {
	storage          *storage.Service
	ticker           *time.Ticker
	stopChan         chan struct{}
	lastArchivedDate string // 格式: "2025-01" - 记录上次归档的月份
}

// NewMonthlyArchiver 创建月度归档器
func NewMonthlyArchiver(storage *storage.Service, checkInterval time.Duration) *MonthlyArchiver {
	return &MonthlyArchiver{
		storage:  storage,
		ticker:   time.NewTicker(checkInterval),
		stopChan: make(chan struct{}),
	}
}

// Start 启动月度归档定期任务
func (m *MonthlyArchiver) Start() {
	log.Println("Starting monthly statistics archiver...")

	// 启动时立即归档上个月的数据（如果还没有归档）
	m.archiveLastMonth()

	go func() {
		for {
			select {
			case <-m.ticker.C:
				m.checkAndArchive()
			case <-m.stopChan:
				log.Println("Monthly statistics archiver stopped")
				return
			}
		}
	}()
}

// Stop 停止归档任务
func (m *MonthlyArchiver) Stop() {
	close(m.stopChan)
	m.ticker.Stop()
}

// checkAndArchive 检查并归档统计数据
func (m *MonthlyArchiver) checkAndArchive() {
	now := time.Now()
	lastMonth := now.AddDate(0, -1, 0)
	lastMonthKey := lastMonth.Format("2006-01")

	// 如果是每月的第一天，且上个月还未归档，则归档上个月的数据
	if now.Day() == 1 && m.lastArchivedDate != lastMonthKey {
		m.archiveLastMonth()
		m.lastArchivedDate = lastMonthKey
	}

	// 每天都归档当前月份（实时更新）
	m.archiveCurrentMonth()
}

// archiveLastMonth 归档上个月的数据
func (m *MonthlyArchiver) archiveLastMonth() {
	now := time.Now()
	lastMonth := now.AddDate(0, -1, 0)
	year, month := lastMonth.Year(), int(lastMonth.Month())

	log.Printf("Archiving monthly stats for %d-%02d...", year, month)

	if err := m.storage.ArchiveMonthlyStats(year, month); err != nil {
		log.Printf("Failed to archive monthly stats for %d-%02d: %v", year, month, err)
		return
	}

	log.Printf("Successfully archived monthly stats for %d-%02d", year, month)
}

// archiveCurrentMonth 归档当前月份（实时更新）
func (m *MonthlyArchiver) archiveCurrentMonth() {
	now := time.Now()
	year, month := now.Year(), int(now.Month())

	if err := m.storage.ArchiveMonthlyStats(year, month); err != nil {
		log.Printf("Failed to update current month stats for %d-%02d: %v", year, month, err)
		return
	}

	log.Printf("Updated current month stats for %d-%02d", year, month)
}
