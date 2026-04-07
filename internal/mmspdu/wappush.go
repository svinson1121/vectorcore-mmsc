package mmspdu

import (
	"github.com/vectorcore/vectorcore-mmsc/internal/wappush"
)

func WrapWAPPush(mmsPDU []byte) []byte {
	return wappush.WrapMMSPDU(mmsPDU)
}

func SegmentWAPPush(push []byte, reference byte) [][]byte {
	segments, _ := wappush.SegmentBinarySMS(push, reference)
	return segments
}
