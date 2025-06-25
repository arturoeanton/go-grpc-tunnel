package proto

import (
	"time"
)

// Helper functions for backward compatibility and performance optimization

// SetErrorMessage sets error message using the optimized field while maintaining metadata compatibility
func (f *Frame) SetErrorMessage(msg string) {
	f.ErrorMessage = &msg
	// Keep metadata for backward compatibility
	if f.Metadata == nil {
		f.Metadata = make(map[string]string)
	}
	f.Metadata["error"] = msg
}

// GetOptimizedErrorMessage gets error message from optimized field or falls back to metadata
func (f *Frame) GetOptimizedErrorMessage() string {
	if f.ErrorMessage != nil {
		return *f.ErrorMessage
	}
	if f.Metadata != nil {
		return f.Metadata["error"]
	}
	return ""
}

// SetCloseReason sets close reason using the optimized field while maintaining metadata compatibility
func (f *Frame) SetCloseReason(reason string) {
	f.CloseReason = &reason
	// Keep metadata for backward compatibility
	if f.Metadata == nil {
		f.Metadata = make(map[string]string)
	}
	f.Metadata["reason"] = reason
}

// GetOptimizedCloseReason gets close reason from optimized field or falls back to metadata
func (f *Frame) GetOptimizedCloseReason() string {
	if f.CloseReason != nil {
		return *f.CloseReason
	}
	if f.Metadata != nil {
		return f.Metadata["reason"]
	}
	return ""
}

// SetTimestamp sets the timestamp field for debugging/monitoring
func (f *Frame) SetTimestamp() {
	now := time.Now().Unix()
	f.Timestamp = &now
}

// NewDataFrame creates a new DATA frame with performance optimizations
func NewDataFrame(connectionID string, payload []byte) *Frame {
	frame := &Frame{
		Type:         FrameType_DATA,
		ConnectionId: connectionID,
		Payload:      payload,
	}
	frame.SetTimestamp()
	return frame
}

// NewErrorFrame creates a new ERROR frame with performance optimizations
func NewErrorFrame(connectionID, errorMsg string) *Frame {
	frame := &Frame{
		Type:         FrameType_ERROR,
		ConnectionId: connectionID,
	}
	frame.SetErrorMessage(errorMsg)
	frame.SetTimestamp()
	return frame
}

// NewCloseFrame creates a new CLOSE_TUNNEL frame with performance optimizations
func NewCloseFrame(connectionID, reason string) *Frame {
	frame := &Frame{
		Type:         FrameType_CLOSE_TUNNEL,
		ConnectionId: connectionID,
	}
	frame.SetCloseReason(reason)
	frame.SetTimestamp()
	return frame
}

// NewTunnelReadyFrame creates a new TUNNEL_READY frame with performance optimizations
func NewTunnelReadyFrame(connectionID string) *Frame {
	frame := &Frame{
		Type:         FrameType_TUNNEL_READY,
		ConnectionId: connectionID,
	}
	frame.SetTimestamp()
	return frame
}

// NewStartTunnelFrame creates a new START_DATA_TUNNEL frame with performance optimizations
func NewStartTunnelFrame(connectionID string) *Frame {
	frame := &Frame{
		Type:         FrameType_START_DATA_TUNNEL,
		ConnectionId: connectionID,
	}
	frame.SetTimestamp()
	return frame
}