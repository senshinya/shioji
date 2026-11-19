package app

import (
	"time"

	"shioji/internal/domain"
	"shioji/internal/transport"
)

type WorkerUpdatePhase string

type WorkerSchemaConfirmation struct {
	SourceBuild, TargetBuild string
}

const (
	WorkerCurrent      WorkerUpdatePhase = "current"
	WorkerAvailable    WorkerUpdatePhase = "available"
	WorkerNewer        WorkerUpdatePhase = "newer"
	WorkerLegacy       WorkerUpdatePhase = "legacy"
	WorkerWaiting      WorkerUpdatePhase = "waiting"
	WorkerUpdating     WorkerUpdatePhase = "updating"
	WorkerUpdateFailed WorkerUpdatePhase = "failed"
)

type WorkerService struct {
	OperationID, OperationPhase string
	UploadPhase                 transport.WorkerUploadPhase
	Legacy                      *domain.LegacyWorkerInfo
	Background                  *domain.BackgroundService
	Build                       *domain.WorkerBuild
	Available                   *domain.WorkerBuild
	Phase                       WorkerUpdatePhase
	Message                     string
}

func newWorkerService(current, available *domain.WorkerBuild) *WorkerService {
	s := &WorkerService{Build: current, Available: available, Phase: WorkerCurrent}
	if current == nil || current.BuildID == "" {
		s.Phase = WorkerLegacy
		s.Message = "执行服务未报告版本，无法确认安全更新能力"
		return s
	}
	if available == nil || current.BuildID == available.BuildID {
		return s
	}
	if current.BuiltAt > 0 && available.BuiltAt > 0 && current.BuiltAt > available.BuiltAt {
		s.Phase = WorkerNewer
		return s
	}
	if !current.SafeUpdate {
		s.Phase = WorkerLegacy
		s.Message = "旧版不支持安全更新，需要手动重启执行服务"
		return s
	}
	s.Phase = WorkerAvailable
	return s
}
func (s *WorkerService) Automatic() bool {
	return s != nil && (s.Phase == WorkerAvailable || s.Phase == WorkerWaiting) && s.Build != nil && s.Available != nil && s.Build.Schema == s.Available.Schema && s.Build.SafeUpdate && s.Build.BuiltAt > 0 && s.Available.BuiltAt > s.Build.BuiltAt
}

func (s *WorkerService) RequiresSchemaMigration() bool {
	return s != nil && s.Build != nil && s.Available.MigratesSchema(s.Build.Schema)
}
func (s *WorkerService) Label() string {
	if s == nil {
		return "已就绪"
	}
	switch s.Phase {
	case WorkerAvailable:
		return "有更新"
	case WorkerNewer:
		return "正在使用较新版本"
	case WorkerLegacy:
		if s.Build == nil || s.Build.BuildID == "" {
			return "版本未报告"
		}
		return "需要手动更新"
	case WorkerWaiting:
		return "等待空闲更新"
	case WorkerUpdating:
		if label := s.UploadLabel(); label != "" {
			return label
		}
		return "正在更新"
	case WorkerUpdateFailed:
		return "更新失败"
	}
	return "已就绪"
}

func (s *WorkerService) UploadLabel() string {
	switch s.UploadPhase {
	case transport.WorkerUploadSending:
		return "正在上传执行服务"
	case transport.WorkerUploadVerifying:
		return "正在校验执行服务"
	case transport.WorkerUploadReconnecting:
		return "等待重连后续传"
	}
	return ""
}
func WorkerBuildLabel(b *domain.WorkerBuild) string {
	if b == nil || b.BuildID == "" {
		return "未报告"
	}
	label := b.BuildID
	if len(label) > 8 {
		label = label[:8]
	}
	if b.BuiltAt > 0 {
		label = time.Unix(b.BuiltAt, 0).Local().Format("01-02 15:04") + " · " + label
	}
	return label
}

// Requested updates may wait safely, or have an unconfirmed send. Reconnection
// must never turn an unconfirmed send back into permission to dispatch it.
type workerUpdateIntent uint8

const (
	updateNotRequested workerUpdateIntent = iota
	updateWaitingForIdle
	updateAwaitingReceipt
)
