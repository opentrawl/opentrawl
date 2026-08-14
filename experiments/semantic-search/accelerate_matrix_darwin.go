package main

/*
#cgo LDFLAGS: -framework Accelerate
#include <Accelerate/Accelerate.h>
*/
import "C"

import "unsafe"

func multiplyNormalizedDocumentAndQueryVectors(documentVectors []float32, documentCount int, queryVectors []float32, queryCount int, dimensions int) []float32 {
	scores := make([]float32, documentCount*queryCount)
	C.cblas_sgemm(
		C.CblasRowMajor,
		C.CblasNoTrans,
		C.CblasTrans,
		C.int(documentCount),
		C.int(queryCount),
		C.int(dimensions),
		1,
		(*C.float)(unsafe.Pointer(&documentVectors[0])),
		C.int(dimensions),
		(*C.float)(unsafe.Pointer(&queryVectors[0])),
		C.int(dimensions),
		0,
		(*C.float)(unsafe.Pointer(&scores[0])),
		C.int(queryCount),
	)
	return scores
}
