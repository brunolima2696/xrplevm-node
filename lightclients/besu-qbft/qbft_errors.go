package qbft

import "errors"

var (
	ErrProcessedTimeNotFound   = errors.New("qbft: processed time not found for height")
	ErrProcessedHeightNotFound = errors.New("qbft: processed height not found for height")
	ErrDelayPeriodNotPassed    = errors.New("qbft: connection delay period has not yet passed")
)
