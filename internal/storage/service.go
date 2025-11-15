package storage

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Service 存储服务（管理对象元数据）
type Service struct {
	db *gorm.DB
}

// OperationCounts 后端操作计数
type OperationCounts struct {
	CountA int64
	CountB int64
}

// NewService 创建新的存储服务
func NewService(db *gorm.DB) *Service {
	return &Service{
		db: db,
	}
}

// RecordObject 记录对象信息
func (s *Service) RecordObject(key, bucketName string, size int64, metadata map[string]string) error {
	// 首先检查是否存在已删除的同名对象
	var deletedObj Object
	if err := s.db.Unscoped().Where("`key` = ?", key).Where("`deleted_at` IS NOT NULL").First(&deletedObj).Error; err == nil {
		// 存在已删除的同名对象，永久删除它
		if err := s.db.Unscoped().Delete(&deletedObj).Error; err != nil {
			return fmt.Errorf("failed to permanently delete soft-deleted object: %w", err)
		}
	}

	obj := &Object{
		Key:        key,
		BucketName: bucketName,
		Size:       size,
	}

	if len(metadata) > 0 {
		obj.Metadata = make(JSON)
		for k, v := range metadata {
			obj.Metadata[k] = v
		}
	} else {
		obj.Metadata = make(JSON)
	}

	// 使用 Upsert（更新或插入）
	result := s.db.Where("`key` = ?", key).Where("`deleted_at` IS NULL").FirstOrCreate(&obj)
	if result.Error != nil {
		return fmt.Errorf("failed to record object: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		// 对象已存在，更新它
		updates := map[string]interface{}{
			"bucket_name": bucketName,
			"size":        size,
			"metadata":    obj.Metadata,
			"updated_at":  time.Now(),
		}
		if err := s.db.Model(&Object{}).Where("`key` = ?", key).Updates(updates).Error; err != nil {
			return fmt.Errorf("failed to update object: %w", err)
		}
	}

	// 更新存储桶统计
	s.updateBucketStats(bucketName)

	return nil
}

// FindObjectBucket 查找对象所在的存储桶
func (s *Service) FindObjectBucket(key string) (string, error) {
	var obj Object
	if err := s.db.Where("`key` = ?", key).First(&obj).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", fmt.Errorf("object not found: %s", key)
		}
		return "", fmt.Errorf("failed to find object: %w", err)
	}
	return obj.BucketName, nil
}

// GetObjectInfo 获取对象信息
func (s *Service) GetObjectInfo(key string) (*Object, error) {
	var obj Object
	if err := s.db.Where("`key` = ?", key).First(&obj).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("object not found: %s", key)
		}
		return nil, fmt.Errorf("failed to get object info: %w", err)
	}
	return &obj, nil
}

// DeleteObject 删除对象记录（软删除）
func (s *Service) DeleteObject(key string) error {
	var obj Object
	if err := s.db.Where("`key` = ?", key).First(&obj).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("object not found: %s", key)
		}
		return fmt.Errorf("failed to find object: %w", err)
	}

	bucketName := obj.BucketName

	// 软删除
	if err := s.db.Delete(&obj).Error; err != nil {
		return fmt.Errorf("failed to delete object: %w", err)
	}

	// 更新存储桶统计
	s.updateBucketStats(bucketName)

	return nil
}

// ListObjects 列出对象（支持S3兼容的参数）
func (s *Service) ListObjects(bucketName, prefix, marker string, maxKeys int) ([]*Object, error) {
	var objects []*Object
	query := s.db.Model(&Object{})

	// 按bucket过滤
	if bucketName != "" {
		query = query.Where("bucket_name = ?", bucketName)
	}

	// 前缀过滤
	if prefix != "" {
		query = query.Where("`key` LIKE ?", prefix+"%")
	}

	// Marker分页
	if marker != "" {
		query = query.Where("`key` > ?", marker)
	}

	// 限制返回数量
	if maxKeys > 0 {
		query = query.Limit(maxKeys)
	}

	// 按key字母顺序排序（S3标准）
	if err := query.Order("`key` ASC").Find(&objects).Error; err != nil {
		return nil, fmt.Errorf("failed to list objects: %w", err)
	}

	return objects, nil
}

// GetBucketObjects 获取特定存储桶的所有对象
func (s *Service) GetBucketObjects(bucketName string) ([]*Object, error) {
	var objects []*Object
	if err := s.db.Where("bucket_name = ?", bucketName).Find(&objects).Error; err != nil {
		return nil, fmt.Errorf("failed to get bucket objects: %w", err)
	}
	return objects, nil
}

// GetTotalSize 获取所有对象的总大小
func (s *Service) GetTotalSize() (int64, error) {
	var total int64
	if err := s.db.Model(&Object{}).Select("COALESCE(SUM(size), 0)").Scan(&total).Error; err != nil {
		return 0, fmt.Errorf("failed to get total size: %w", err)
	}
	return total, nil
}

// GetBucketSize 获取特定存储桶的总大小
func (s *Service) GetBucketSize(bucketName string) (int64, error) {
	var total int64
	if err := s.db.Model(&Object{}).
		Where("bucket_name = ?", bucketName).
		Select("COALESCE(SUM(size), 0)").
		Scan(&total).Error; err != nil {
		return 0, fmt.Errorf("failed to get bucket size: %w", err)
	}
	return total, nil
}

// GetObjectCount 获取对象总数
func (s *Service) GetObjectCount() (int64, error) {
	var count int64
	if err := s.db.Model(&Object{}).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("failed to get object count: %w", err)
	}
	return count, nil
}

// GetBucketObjectCount 获取特定存储桶的对象数
func (s *Service) GetBucketObjectCount(bucketName string) (int64, error) {
	var count int64
	if err := s.db.Model(&Object{}).
		Where("bucket_name = ?", bucketName).
		Count(&count).Error; err != nil {
		return 0, fmt.Errorf("failed to get bucket object count: %w", err)
	}
	return count, nil
}

// updateBucketStats 更新存储桶统计信息
func (s *Service) updateBucketStats(bucketName string) error {
	var stats BucketStats

	// 计算新的统计数据
	var count int64
	var totalSize int64

	if err := s.db.Model(&Object{}).
		Where("bucket_name = ?", bucketName).
		Count(&count).Error; err != nil {
		return fmt.Errorf("failed to count objects: %w", err)
	}

	if err := s.db.Model(&Object{}).
		Where("bucket_name = ?", bucketName).
		Select("COALESCE(SUM(size), 0)").
		Scan(&totalSize).Error; err != nil {
		return fmt.Errorf("failed to sum object sizes: %w", err)
	}

	// 尝试查找现有记录
	err := s.db.Where("bucket_name = ?", bucketName).First(&stats).Error

	if err == gorm.ErrRecordNotFound {
		// 记录不存在，创建新记录
		stats = BucketStats{
			BucketName:    bucketName,
			ObjectCount:   count,
			TotalSize:     totalSize,
			LastCheckedAt: time.Now(),
		}
		if err := s.db.Create(&stats).Error; err != nil {
			return fmt.Errorf("failed to create bucket stats: %w", err)
		}
	} else if err != nil {
		// 其他数据库错误
		return fmt.Errorf("failed to query bucket stats: %w", err)
	} else {
		// 记录存在，更新统计数据
		updates := map[string]interface{}{
			"object_count":    count,
			"total_size":      totalSize,
			"last_checked_at": time.Now(),
		}

		if err := s.db.Model(&stats).Updates(updates).Error; err != nil {
			return fmt.Errorf("failed to update bucket stats: %w", err)
		}
	}

	return nil
}

func (s *Service) ensureBucketStats(bucketName string) (*BucketStats, error) {
	if bucketName == "" {
		return nil, fmt.Errorf("bucket name cannot be empty")
	}

	stats := &BucketStats{}
	err := s.db.Where("bucket_name = ?", bucketName).First(stats).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		stats = &BucketStats{BucketName: bucketName, LastCheckedAt: time.Now()}
		if createErr := s.db.Create(stats).Error; createErr != nil {
			// 如果在并发创建下出现重复键，忽略并再次查询
			if errors.Is(createErr, gorm.ErrDuplicatedKey) {
				if retryErr := s.db.Where("bucket_name = ?", bucketName).First(stats).Error; retryErr != nil {
					return nil, fmt.Errorf("failed to fetch bucket stats after duplicate: %w", retryErr)
				}
			} else {
				return nil, fmt.Errorf("failed to create bucket stats: %w", createErr)
			}
		}
		return stats, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to fetch bucket stats: %w", err)
	}

	return stats, nil
}

// IncrementBucketOperation 增加指定存储桶的操作计数
func (s *Service) IncrementBucketOperation(bucketName, category string) (int64, error) {
	if _, err := s.ensureBucketStats(bucketName); err != nil {
		return 0, err
	}

	var field string
	switch category {
	case "A":
		field = "operation_count_a"
	case "B":
		field = "operation_count_b"
	default:
		return 0, fmt.Errorf("unknown operation category: %s", category)
	}

	// 使用事务确保原子性
	var count int64
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// 原子递增
		if err := tx.Model(&BucketStats{}).
			Where("bucket_name = ?", bucketName).
			UpdateColumn(field, gorm.Expr(field+" + ?", 1)).Error; err != nil {
			return fmt.Errorf("failed to increment %s for bucket %s: %w", field, bucketName, err)
		}

		// 在同一事务中读取最新值
		if err := tx.Model(&BucketStats{}).
			Where("bucket_name = ?", bucketName).
			Select(field).
			Scan(&count).Error; err != nil {
			return fmt.Errorf("failed to fetch updated %s for bucket %s: %w", field, bucketName, err)
		}

		return nil
	})

	if err != nil {
		return 0, err
	}

	return count, nil
}

// GetBucketOperationCounts 获取所有存储桶的操作计数
func (s *Service) GetBucketOperationCounts() (map[string]OperationCounts, error) {
	var stats []BucketStats
	if err := s.db.Find(&stats).Error; err != nil {
		return nil, fmt.Errorf("failed to list bucket stats: %w", err)
	}

	result := make(map[string]OperationCounts, len(stats))
	for _, st := range stats {
		result[st.BucketName] = OperationCounts{
			CountA: st.OperationCountA,
			CountB: st.OperationCountB,
		}
	}

	return result, nil
}

// RecordUploadSession 记录上传会话
func (s *Service) RecordUploadSession(uploadID, key, bucketName string, size int64) error {
	session := &UploadSession{
		UploadID:   uploadID,
		Key:        key,
		BucketName: bucketName,
		Size:       size,
		Status:     "pending",
	}

	if err := s.db.Create(session).Error; err != nil {
		return fmt.Errorf("failed to record upload session: %w", err)
	}

	return nil
}

// GetUploadSession 获取上传会话
func (s *Service) GetUploadSession(uploadID string) (*UploadSession, error) {
	var session UploadSession
	if err := s.db.Where("upload_id = ?", uploadID).First(&session).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("upload session not found: %s", uploadID)
		}
		return nil, fmt.Errorf("failed to get upload session: %w", err)
	}
	return &session, nil
}

// UpdateUploadSession 更新上传会话
func (s *Service) UpdateUploadSession(uploadID string, completedParts int, status string) error {
	updates := map[string]interface{}{
		"completed_parts": completedParts,
		"status":          status,
		"updated_at":      time.Now(),
	}

	if err := s.db.Model(&UploadSession{}).
		Where("upload_id = ?", uploadID).
		Updates(updates).Error; err != nil {
		return fmt.Errorf("failed to update upload session: %w", err)
	}

	return nil
}

// IncrementUploadSessionSize 增加上传会话的大小（用于累加分片大小）
func (s *Service) IncrementUploadSessionSize(uploadID string, partSize int64) error {
	if err := s.db.Model(&UploadSession{}).
		Where("upload_id = ?", uploadID).
		UpdateColumn("size", gorm.Expr("size + ?", partSize)).Error; err != nil {
		return fmt.Errorf("failed to increment upload session size: %w", err)
	}
	return nil
}

// GetUploadSessionSize 获取上传会话当前累积的大小
func (s *Service) GetUploadSessionSize(uploadID string) (int64, error) {
	var session UploadSession
	if err := s.db.Select("size").Where("upload_id = ?", uploadID).First(&session).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return 0, fmt.Errorf("upload session not found: %s", uploadID)
		}
		return 0, fmt.Errorf("failed to get upload session size: %w", err)
	}
	return session.Size, nil
}

// GetPendingUploadSessions 获取正在进行中的上传会话
func (s *Service) GetPendingUploadSessions(prefix string, keyMarker string, uploadIdMarker string, maxUploads int) ([]*UploadSession, error) {
	query := s.db.Model(&UploadSession{}).Where("status = ?", "pending")

	// 根据前缀过滤
	if prefix != "" {
		query = query.Where("`key` LIKE ?", prefix+"%")
	}

	// 分页标记处理
	if keyMarker != "" {
		if uploadIdMarker != "" {
			// 如果同时指定了key和uploadId标记
			query = query.Where("(`key` > ? OR (`key` = ? AND upload_id > ?))", keyMarker, keyMarker, uploadIdMarker)
		} else {
			query = query.Where("`key` > ?", keyMarker)
		}
	}

	// 限制返回数量
	if maxUploads > 0 {
		query = query.Limit(maxUploads + 1) // 多查询一个以判断是否截断
	}

	// 按key和uploadID排序
	query = query.Order("`key` ASC, upload_id ASC")

	var sessions []*UploadSession
	if err := query.Find(&sessions).Error; err != nil {
		return nil, fmt.Errorf("failed to get pending upload sessions: %w", err)
	}

	return sessions, nil
}

// CleanExpiredSessions 清理过期的上传会话
func (s *Service) CleanExpiredSessions() error {
	if err := s.db.Where("expires_at < ? AND status = ?", time.Now(), "pending").
		Delete(&UploadSession{}).Error; err != nil {
		return fmt.Errorf("failed to clean expired sessions: %w", err)
	}
	return nil
}

// RecordAccessLog 记录访问日志
func (s *Service) RecordAccessLog(action, key, bucketName, clientIP, userAgent, host string, size int64, success bool, errorMsg string, responseTime int64) error {
	log := &AccessLog{
		Action:       action,
		Key:          key,
		BucketName:   bucketName,
		ClientIP:     clientIP,
		UserAgent:    userAgent,
		Host:         host,
		Size:         size,
		Success:      success,
		ErrorMsg:     errorMsg,
		ResponseTime: responseTime,
	}

	if err := s.db.Create(log).Error; err != nil {
		return fmt.Errorf("failed to record access log: %w", err)
	}

	return nil
}

// GetAccessLogs 获取访问日志
func (s *Service) GetAccessLogs(filter *AccessLogFilter) ([]*AccessLog, error) {
	query := s.db.Model(&AccessLog{})

	if filter != nil {
		if filter.Action != "" {
			query = query.Where("action = ?", filter.Action)
		}
		if filter.Key != "" {
			query = query.Where("`key` = ?", filter.Key)
		}
		if filter.BucketName != "" {
			query = query.Where("bucket_name = ?", filter.BucketName)
		}
		if filter.ClientIP != "" {
			query = query.Where("client_ip = ?", filter.ClientIP)
		}
		if filter.Success != nil {
			query = query.Where("success = ?", *filter.Success)
		}
		if !filter.StartTime.IsZero() {
			query = query.Where("created_at >= ?", filter.StartTime)
		}
		if !filter.EndTime.IsZero() {
			query = query.Where("created_at <= ?", filter.EndTime)
		}
		if filter.Limit > 0 {
			query = query.Limit(filter.Limit)
		}
		if filter.Offset > 0 {
			query = query.Offset(filter.Offset)
		}
	}

	var logs []*AccessLog
	if err := query.Order("created_at DESC").Find(&logs).Error; err != nil {
		return nil, fmt.Errorf("failed to get access logs: %w", err)
	}

	return logs, nil
}

// CreateVirtualBucketMapping 创建虚拟存储桶文件级映射
func (s *Service) CreateVirtualBucketMapping(virtualBucketName, objectKey, realBucketName string) error {
	mapping := &VirtualBucketMapping{
		VirtualBucketName: virtualBucketName,
		ObjectKey:         objectKey,
		RealBucketName:    realBucketName,
	}

	if err := s.db.Create(mapping).Error; err != nil {
		return fmt.Errorf("failed to create virtual bucket mapping: %w", err)
	}

	return nil
}

// GetVirtualBucketMapping 获取虚拟存储桶文件级映射
func (s *Service) GetVirtualBucketMapping(virtualBucketName, objectKey string) (*VirtualBucketMapping, error) {
	var mapping VirtualBucketMapping
	if err := s.db.Where("virtual_bucket_name = ? AND object_key = ?", virtualBucketName, objectKey).First(&mapping).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("virtual bucket mapping not found: %s/%s", virtualBucketName, objectKey)
		}
		return nil, fmt.Errorf("failed to get virtual bucket mapping: %w", err)
	}
	return &mapping, nil
}

// GetVirtualBucketMappings 获取所有虚拟存储桶映射
func (s *Service) GetVirtualBucketMappings() ([]*VirtualBucketMapping, error) {
	var mappings []*VirtualBucketMapping
	if err := s.db.Find(&mappings).Error; err != nil {
		return nil, fmt.Errorf("failed to get virtual bucket mappings: %w", err)
	}
	return mappings, nil
}

// GetVirtualBucketMappingsForBucket 获取指定虚拟存储桶的所有映射
func (s *Service) GetVirtualBucketMappingsForBucket(virtualBucketName string) ([]*VirtualBucketMapping, error) {
	var mappings []*VirtualBucketMapping
	if err := s.db.Where("virtual_bucket_name = ?", virtualBucketName).Find(&mappings).Error; err != nil {
		return nil, fmt.Errorf("failed to get virtual bucket mappings for bucket %s: %w", virtualBucketName, err)
	}
	return mappings, nil
}

// UpdateVirtualBucketMapping 更新虚拟存储桶映射
func (s *Service) UpdateVirtualBucketMapping(virtualBucketName, objectKey, realBucketName string) error {
	updates := map[string]interface{}{
		"real_bucket_name": realBucketName,
		"updated_at":       time.Now(),
	}

	if err := s.db.Model(&VirtualBucketMapping{}).
		Where("virtual_bucket_name = ? AND object_key = ?", virtualBucketName, objectKey).
		Updates(updates).Error; err != nil {
		return fmt.Errorf("failed to update virtual bucket mapping: %w", err)
	}

	return nil
}

// DeleteVirtualBucketMapping 删除虚拟存储桶映射
func (s *Service) DeleteVirtualBucketMapping(virtualBucketName string) error {
	// 删除虚拟存储桶的所有映射
	if err := s.db.Where("virtual_bucket_name = ?", virtualBucketName).
		Delete(&VirtualBucketMapping{}).Error; err != nil {
		return fmt.Errorf("failed to delete virtual bucket mapping: %w", err)
	}
	return nil
}

// DeleteVirtualBucketObjectMapping 删除虚拟存储桶中特定对象的映射
func (s *Service) DeleteVirtualBucketObjectMapping(virtualBucketName, objectKey string) error {
	if err := s.db.Where("virtual_bucket_name = ? AND object_key = ?", virtualBucketName, objectKey).
		Delete(&VirtualBucketMapping{}).Error; err != nil {
		return fmt.Errorf("failed to delete virtual bucket object mapping: %w", err)
	}
	return nil
}

// GetVirtualBucketObjects 获取虚拟存储桶中的所有对象
func (s *Service) GetVirtualBucketObjects(virtualBucketName string) ([]*Object, error) {
	// 获取虚拟存储桶的所有文件映射
	mappings, err := s.GetVirtualBucketMappingsForBucket(virtualBucketName)
	if err != nil {
		return nil, err
	}

	if len(mappings) == 0 {
		return []*Object{}, nil
	}

	// 收集所有对象键
	objectKeys := make([]string, 0, len(mappings))
	for _, mapping := range mappings {
		objectKeys = append(objectKeys, mapping.ObjectKey)
	}

	// 从对象表中查询这些对象
	var objects []*Object
	if err := s.db.Where("`key` IN ?", objectKeys).Find(&objects).Error; err != nil {
		return nil, fmt.Errorf("failed to get objects for virtual bucket: %w", err)
	}

	return objects, nil
}

// DeleteVirtualBucketFileMapping 删除虚拟存储桶文件映射
func (s *Service) DeleteVirtualBucketFileMapping(virtualBucketName, objectKey string) error {
	if err := s.db.Where("virtual_bucket_name = ? AND object_key = ?", virtualBucketName, objectKey).
		Delete(&VirtualBucketMapping{}).Error; err != nil {
		return fmt.Errorf("failed to delete virtual bucket file mapping: %w", err)
	}
	return nil
}

// ArchiveMonthlyStats 归档指定月份的统计数据（存储增量值，非累计值）
// 如果该月份的记录已存在，则更新；否则创建新记录
func (s *Service) ArchiveMonthlyStats(year, month int) error {
	// 获取当前所有bucket的累计统计
	var currentStats []BucketStats
	if err := s.db.Find(&currentStats).Error; err != nil {
		return fmt.Errorf("failed to fetch bucket stats: %w", err)
	}

	// 获取上个月的累计值（从上月归档数据推算）
	lastYear, lastMonth := year, month-1
	if lastMonth == 0 {
		lastMonth = 12
		lastYear--
	}

	// 查询上个月及之前的所有归档数据，用于推算上月末的累计值
	var lastMonthArchived []BucketMonthlyStats
	lastMonthMap := make(map[string]int64) // bucket_name -> last_month_cumulative_a
	lastMonthMapB := make(map[string]int64) // bucket_name -> last_month_cumulative_b

	if err := s.db.Where("year < ? OR (year = ? AND month <= ?)", lastYear, lastYear, lastMonth).
		Order("year ASC, month ASC").
		Find(&lastMonthArchived).Error; err == nil {

		// 累加历史增量得到上月末累计值
		cumulativeA := make(map[string]int64)
		cumulativeB := make(map[string]int64)

		for _, archived := range lastMonthArchived {
			cumulativeA[archived.BucketName] += archived.OperationCountA
			cumulativeB[archived.BucketName] += archived.OperationCountB
		}

		lastMonthMap = cumulativeA
		lastMonthMapB = cumulativeB
	}

	// 对每个bucket，计算本月增量并存储
	for _, stat := range currentStats {
		lastCumulativeA := lastMonthMap[stat.BucketName]
		lastCumulativeB := lastMonthMapB[stat.BucketName]

		incrementA := stat.OperationCountA - lastCumulativeA
		incrementB := stat.OperationCountB - lastCumulativeB

		// 如果是首次运行（没有历史数据），incrementA/B 可能等于累计值
		// 这是预期行为：首月记录的就是从0到当前的增量

		// 边界情况：如果计算出负值，说明数据不一致，设置为0
		if incrementA < 0 {
			incrementA = 0
		}
		if incrementB < 0 {
			incrementB = 0
		}

		monthlyStats := BucketMonthlyStats{
			BucketName:      stat.BucketName,
			Year:            year,
			Month:           month,
			OperationCountA: incrementA,
			OperationCountB: incrementB,
		}

		// 使用 UPSERT 逻辑：如果存在则更新，否则创建
		if err := s.db.Where("bucket_name = ? AND year = ? AND month = ?",
			stat.BucketName, year, month).
			Assign(BucketMonthlyStats{
				OperationCountA: incrementA,
				OperationCountB: incrementB,
			}).
			FirstOrCreate(&monthlyStats).Error; err != nil {
			return fmt.Errorf("failed to archive monthly stats for bucket %s: %w", stat.BucketName, err)
		}
	}

	return nil
}

// GetMonthlyStats 获取指定月份的统计数据
func (s *Service) GetMonthlyStats(year, month int) ([]BucketMonthlyStats, error) {
	var stats []BucketMonthlyStats
	if err := s.db.Where("year = ? AND month = ?", year, month).
		Find(&stats).Error; err != nil {
		return nil, fmt.Errorf("failed to fetch monthly stats: %w", err)
	}
	return stats, nil
}

// GetMonthlyStatsRange 获取指定时间范围的统计数据
func (s *Service) GetMonthlyStatsRange(startYear, startMonth, endYear, endMonth int) ([]BucketMonthlyStats, error) {
	var stats []BucketMonthlyStats
	if err := s.db.Where("(year > ? OR (year = ? AND month >= ?)) AND (year < ? OR (year = ? AND month <= ?))",
		startYear, startYear, startMonth, endYear, endYear, endMonth).
		Order("year, month, bucket_name").
		Find(&stats).Error; err != nil {
		return nil, fmt.Errorf("failed to fetch monthly stats range: %w", err)
	}
	return stats, nil
}

// GetCurrentMonthStats 获取当前月份的实时统计（从 bucket_stats 计算增量）
func (s *Service) GetCurrentMonthStats() ([]BucketMonthlyStats, error) {
	now := time.Now()
	year, month := now.Year(), int(now.Month())

	// 获取上个月末的累计值（通过累加所有历史增量）
	lastYear, lastMonth := year, month-1
	if lastMonth == 0 {
		lastMonth = 12
		lastYear--
	}

	var historicalStats []BucketMonthlyStats
	lastMonthCumulativeA := make(map[string]int64)
	lastMonthCumulativeB := make(map[string]int64)

	if err := s.db.Where("year < ? OR (year = ? AND month <= ?)", lastYear, lastYear, lastMonth).
		Find(&historicalStats).Error; err == nil {
		// 累加所有历史增量得到上月末累计值
		for _, stat := range historicalStats {
			lastMonthCumulativeA[stat.BucketName] += stat.OperationCountA
			lastMonthCumulativeB[stat.BucketName] += stat.OperationCountB
		}
	}

	// 获取当前累计数据
	var currentStats []BucketStats
	if err := s.db.Find(&currentStats).Error; err != nil {
		return nil, fmt.Errorf("failed to fetch current bucket stats: %w", err)
	}

	// 计算当前月份的增量
	result := make([]BucketMonthlyStats, 0, len(currentStats))
	for _, current := range currentStats {
		incrementA := current.OperationCountA - lastMonthCumulativeA[current.BucketName]
		incrementB := current.OperationCountB - lastMonthCumulativeB[current.BucketName]

		// 边界情况：如果计算出负值，说明数据不一致，设置为0
		if incrementA < 0 {
			incrementA = 0
		}
		if incrementB < 0 {
			incrementB = 0
		}

		result = append(result, BucketMonthlyStats{
			BucketName:      current.BucketName,
			Year:            year,
			Month:           month,
			OperationCountA: incrementA,
			OperationCountB: incrementB,
			UpdatedAt:       time.Now(),
		})
	}

	return result, nil
}

// GetBucketMonthlyHistory 获取指定存储桶的月度历史统计
func (s *Service) GetBucketMonthlyHistory(bucketName string, months int) ([]BucketMonthlyStats, error) {
	var stats []BucketMonthlyStats
	if err := s.db.Where("bucket_name = ?", bucketName).
		Order("year DESC, month DESC").
		Limit(months).
		Find(&stats).Error; err != nil {
		return nil, fmt.Errorf("failed to fetch bucket monthly history: %w", err)
	}
	return stats, nil
}
