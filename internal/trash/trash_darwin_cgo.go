//go:build darwin && cgo

package trash

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>
#include <string.h>
#import <Foundation/Foundation.h>

// Returns 1 on success. *out and *errOut are malloc'd; the caller frees them.
static int cc_trash(const char *path, char **out, char **errOut) {
	@autoreleasepool {
		NSFileManager *fm = [NSFileManager defaultManager];
		NSString *p = [fm stringWithFileSystemRepresentation:path length:strlen(path)];
		if (p == nil) {
			*errOut = strdup("path is not valid for this filesystem");
			return 0;
		}
		NSURL *url = [NSURL fileURLWithPath:p];
		NSURL *result = nil;
		NSError *err = nil;
		if (![fm trashItemAtURL:url resultingItemURL:&result error:&err]) {
			NSString *msg = @"unknown error";
			if (err != nil) {
				msg = [NSString stringWithFormat:@"%@ (%@ %ld)",
					err.localizedDescription, err.domain, (long)err.code];
			}
			const char *c = [msg UTF8String];
			*errOut = strdup(c != NULL ? c : "unknown error");
			return 0;
		}
		if (result != nil) {
			const char *r = [result fileSystemRepresentation];
			if (r != NULL) {
				*out = strdup(r);
			}
		}
		return 1;
	}
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

type finderTrash struct{}

func New() Trasher { return finderTrash{} }

func (finderTrash) Trash(path string) (string, error) {
	if err := checkPath(path); err != nil {
		return "", err
	}
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var out, errOut *C.char
	ok := C.cc_trash(cpath, &out, &errOut)
	if out != nil {
		defer C.free(unsafe.Pointer(out))
	}
	if errOut != nil {
		defer C.free(unsafe.Pointer(errOut))
	}
	if ok == 0 {
		return "", fmt.Errorf("move %s to Trash: %s", path, C.GoString(errOut))
	}
	return C.GoString(out), nil
}
