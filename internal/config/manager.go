package config

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
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

// UpdateConfig 通过 API 更新配置文件
// 返回错误如果验证失败或写入失败
func (m *Manager) UpdateConfig(newConfig *Config) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// 1. 验证新配置
	if err := m.validateConfig(newConfig); err != nil {
		return err
	}

	// 2. 备份当前配置文件
	if err := m.backupConfigFile(); err != nil {
		log.Printf("Failed to backup config file: %v", err)
		// 继续执行，备份失败不应阻止更新
	}

	// 3. 将新配置写入文件
	if err := m.writeConfigFile(newConfig); err != nil {
		return err
	}

	// 4. 更新内存中的配置
	oldConfig := m.config
	m.config = newConfig

	// 5. 更新最后修改时间，避免文件监听重复触发
	if fileInfo, err := os.Stat(m.configFile); err == nil {
		m.lastModTime = fileInfo.ModTime()
	}

	log.Printf("Configuration updated successfully via API")

	// 6. 触发配置变更回调（在锁外执行）
	callbacks := make([]func(*Config), len(m.callbacks))
	copy(callbacks, m.callbacks)

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

	// 7. 记录配置变更
	m.logConfigChanges(oldConfig, newConfig)

	return nil
}

// validateConfig 验证配置的有效性
func (m *Manager) validateConfig(cfg *Config) error {
	// 基本验证
	if cfg.Server.Port <= 0 || cfg.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", cfg.Server.Port)
	}

	if len(cfg.Buckets) == 0 {
		return fmt.Errorf("at least one bucket is required")
	}

	// 验证存储桶配置
	for i, bucket := range cfg.Buckets {
		if bucket.Name == "" {
			return fmt.Errorf("bucket[%d]: name is required", i)
		}

		// 虚拟存储桶不需要端点和凭据
		if !bucket.Virtual {
			if bucket.Endpoint == "" {
				return fmt.Errorf("bucket[%d] (%s): endpoint is required for non-virtual bucket", i, bucket.Name)
			}
			if bucket.AccessKeyID == "" {
				return fmt.Errorf("bucket[%d] (%s): access_key_id is required for non-virtual bucket", i, bucket.Name)
			}
			if bucket.SecretAccessKey == "" {
				return fmt.Errorf("bucket[%d] (%s): secret_access_key is required for non-virtual bucket", i, bucket.Name)
			}
		}

		// 解析并验证容量大小
		if err := cfg.Buckets[i].ParseMaxSize(); err != nil {
			return fmt.Errorf("bucket[%d] (%s): invalid max_size: %w", i, bucket.Name, err)
		}
	}

	// 验证负载均衡策略
	validStrategies := map[string]bool{
		"round-robin": true,
		"least-space": true,
		"weighted":    true,
	}
	if !validStrategies[cfg.Balancer.Strategy] {
		return fmt.Errorf("invalid balancer strategy: %s (must be one of: round-robin, least-space, weighted)", cfg.Balancer.Strategy)
	}

	// 验证数据库配置
	if cfg.Database.Type == "" {
		return fmt.Errorf("database type is required")
	}
	validDBTypes := map[string]bool{
		"sqlite":   true,
		"mysql":    true,
		"postgres": true,
	}
	if !validDBTypes[cfg.Database.Type] {
		return fmt.Errorf("invalid database type: %s (must be one of: sqlite, mysql, postgres)", cfg.Database.Type)
	}

	return nil
}

// backupConfigFile 备份当前配置文件
func (m *Manager) backupConfigFile() error {
	backupPath := m.configFile + ".backup." + time.Now().Format("20060102-150405")

	sourceData, err := os.ReadFile(m.configFile)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	if err := os.WriteFile(backupPath, sourceData, 0644); err != nil {
		return fmt.Errorf("failed to write backup file: %w", err)
	}

	log.Printf("Config file backed up to: %s", backupPath)
	return nil
}

// writeConfigFile 将配置写入 YAML 文件
func (m *Manager) writeConfigFile(cfg *Config) error {
	// 临时文件，确保原子性
	tmpFile := m.configFile + ".tmp"

	file, err := os.OpenFile(tmpFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to create temp config file: %w", err)
	}
	defer file.Close()

	encoder := yaml.NewEncoder(file)
	encoder.SetIndent(2)

	if err := encoder.Encode(cfg); err != nil {
		file.Close()
		os.Remove(tmpFile)
		return fmt.Errorf("failed to encode config: %w", err)
	}

	if err := encoder.Close(); err != nil {
		file.Close()
		os.Remove(tmpFile)
		return fmt.Errorf("failed to close encoder: %w", err)
	}

	if err := file.Close(); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// 原子性替换原文件
	if err := os.Rename(tmpFile, m.configFile); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("failed to replace config file: %w", err)
	}

	return nil
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