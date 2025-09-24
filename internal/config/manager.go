package config

import (
	"log"
	"os"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Manager 配置管理器，支持热更新
type Manager struct {
	configFile    string
	config        *Config
	mutex         sync.RWMutex
	watcher       *fsnotify.Watcher
	callbacks     []func(*Config)
	stopChan      chan struct{}
	lastModTime   time.Time
	pollingTicker *time.Ticker
}

// NewManager 创建新的配置管理器
func NewManager(configFile string) (*Manager, error) {
	// 初始加载配置
	cfg, err := Load(configFile)
	if err != nil {
		return nil, err
	}

	// 获取文件的初始修改时间
	fileInfo, err := os.Stat(configFile)
	if err != nil {
		return nil, err
	}

	manager := &Manager{
		configFile:  configFile,
		config:      cfg,
		callbacks:   make([]func(*Config), 0),
		stopChan:    make(chan struct{}),
		lastModTime: fileInfo.ModTime(),
	}

	// 同时启用fsnotify和轮询监听
	// 这样可以确保在Docker挂载等场景下也能正常工作
	manager.initWatching()

	return manager, nil
}

// initWatching 初始化文件监听（同时使用fsnotify和轮询）
func (m *Manager) initWatching() {
	// 尝试启用fsnotify
	watcher, err := fsnotify.NewWatcher()
	if err == nil {
		if err := watcher.Add(m.configFile); err == nil {
			m.watcher = watcher
			log.Println("fsnotify watcher enabled for config file")
			go m.watchConfig()
		} else {
			log.Printf("Failed to add file to fsnotify watcher: %v", err)
			watcher.Close()
		}
	} else {
		log.Printf("Failed to create fsnotify watcher: %v", err)
	}

	// 同时启用轮询模式（作为备用和补充）
	// 在Docker挂载等场景下，轮询更可靠
	m.pollingTicker = time.NewTicker(3 * time.Second)
	log.Println("Config file polling enabled (3s interval)")
	go m.pollConfig()
}

// pollConfig 轮询检查配置文件变化
func (m *Manager) pollConfig() {
	for {
		select {
		case <-m.pollingTicker.C:
			fileInfo, err := os.Stat(m.configFile)
			if err != nil {
				log.Printf("Failed to stat config file during polling: %v", err)
				continue
			}

			// 检查文件修改时间
			if fileInfo.ModTime().After(m.lastModTime) {
				log.Printf("Config file %s modified (detected by polling), reloading...", m.configFile)
				m.lastModTime = fileInfo.ModTime()
				m.reloadConfig()
			}

		case <-m.stopChan:
			return
		}
	}
}

// GetConfig 获取当前配置（线程安全）
func (m *Manager) GetConfig() *Config {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	// 返回配置的副本以避免并发修改
	configCopy := *m.config
	return &configCopy
}

// OnConfigChange 注册配置变化回调
func (m *Manager) OnConfigChange(callback func(*Config)) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.callbacks = append(m.callbacks, callback)
}

// watchConfig 监听配置文件变化（fsnotify模式）
func (m *Manager) watchConfig() {
	for {
		select {
		case event, ok := <-m.watcher.Events:
			if !ok {
				return
			}

			// 只处理修改和重命名事件
			if event.Op&fsnotify.Write == fsnotify.Write ||
			   event.Op&fsnotify.Rename == fsnotify.Rename {
				log.Printf("Config file %s modified (detected by fsnotify), reloading...", m.configFile)

				// 更新最后修改时间以避免轮询重复触发
				if fileInfo, err := os.Stat(m.configFile); err == nil {
					m.lastModTime = fileInfo.ModTime()
				}

				m.reloadConfig()
			}

		case err, ok := <-m.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("Config watcher error: %v", err)

		case <-m.stopChan:
			return
		}
	}
}

// reloadConfig 重新加载配置
func (m *Manager) reloadConfig() {
	// 添加延迟以防止编辑器的多次写入事件
	time.Sleep(100 * time.Millisecond)

	// 加载新配置
	newConfig, err := Load(m.configFile)
	if err != nil {
		log.Printf("Failed to reload config: %v", err)
		return
	}

	// 更新配置
	m.mutex.Lock()
	oldConfig := m.config
	m.config = newConfig
	callbacks := make([]func(*Config), len(m.callbacks))
	copy(callbacks, m.callbacks)
	m.mutex.Unlock()

	log.Printf("Configuration reloaded successfully")

	// 异步调用回调函数
	go func() {
		for _, callback := range callbacks {
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("Config change callback panic: %v", r)
					}
				}()
				callback(newConfig)
			}()
		}
	}()

	// 记录重要配置变更
	m.logConfigChanges(oldConfig, newConfig)
}

// logConfigChanges 记录配置变更
func (m *Manager) logConfigChanges(oldConfig, newConfig *Config) {
	// 检查服务器端口变化
	if oldConfig.Server.Port != newConfig.Server.Port {
		log.Printf("Server port changed: %d -> %d (restart required)",
			oldConfig.Server.Port, newConfig.Server.Port)
	}

	// 检查数据库配置变化
	if oldConfig.Database.DSN != newConfig.Database.DSN {
		log.Printf("Database DSN changed (restart required)")
	}

	// 检查存储桶数量变化
	if len(oldConfig.Buckets) != len(newConfig.Buckets) {
		log.Printf("Bucket count changed: %d -> %d",
			len(oldConfig.Buckets), len(newConfig.Buckets))
	}

	// 检查负载均衡策略变化
	if oldConfig.Balancer.Strategy != newConfig.Balancer.Strategy {
		log.Printf("Load balancer strategy changed: %s -> %s",
			oldConfig.Balancer.Strategy, newConfig.Balancer.Strategy)
	}

	// 检查代理模式变化
	if oldConfig.S3API.ProxyMode != newConfig.S3API.ProxyMode {
		log.Printf("S3 API proxy mode changed: %t -> %t",
			oldConfig.S3API.ProxyMode, newConfig.S3API.ProxyMode)
	}

	// 检查指标配置变化
	if oldConfig.Metrics.Enabled != newConfig.Metrics.Enabled {
		log.Printf("Metrics enabled changed: %t -> %t",
			oldConfig.Metrics.Enabled, newConfig.Metrics.Enabled)
	}
}

// Close 关闭配置管理器
func (m *Manager) Close() error {
	// 停止监听协程
	close(m.stopChan)

	// 停止轮询
	if m.pollingTicker != nil {
		m.pollingTicker.Stop()
	}

	// 关闭fsnotify watcher
	if m.watcher != nil {
		return m.watcher.Close()
	}

	return nil
}