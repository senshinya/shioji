package mobileclient

import (
	"bufio"
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

// OpenWorkerArtifact streams the packaged Mach-O bytes and their expected
// digest without executing or unpacking the binary on the controlling device.
func OpenWorkerArtifact(path string) (io.ReadCloser, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	archive := bufio.NewReaderSize(file, 16<<10)
	build, err := readWorkerManifest(archive)
	if err != nil {
		file.Close()
		return nil, "", err
	}
	reader, err := gzip.NewReader(archive)
	if err != nil {
		file.Close()
		return nil, "", err
	}
	return workerArchive{reader, file}, build.BuildID, nil
}

// InspectWorkerArtifact reads the signed app bundle's manifest without loading
// the Mac executable. Older digest-only bundles can still be used to install.
func InspectWorkerArtifact(path string) (domain.WorkerBuild, error) {
	file, err := os.Open(path)
	if err != nil {
		return domain.WorkerBuild{}, err
	}
	defer file.Close()
	return readWorkerManifest(bufio.NewReaderSize(file, 16<<10))
}

func readWorkerManifest(archive *bufio.Reader) (domain.WorkerBuild, error) {
	line, err := archive.ReadSlice('\n')
	if err != nil {
		return domain.WorkerBuild{}, errors.New("无法读取内置执行服务摘要")
	}
	var build domain.WorkerBuild
	if len(line) == 65 {
		build.BuildID = string(line[:64])
	} else {
		var frame protocol.Frame
		if json.Unmarshal(line, &frame) != nil || frame.Type != "worker-build" || frame.Major != protocol.Major || frame.Worker == nil {
			return build, errors.New("内置执行服务版本信息无效")
		}
		build = *frame.Worker
	}
	digest, err := hex.DecodeString(build.BuildID)
	if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != build.BuildID {
		return domain.WorkerBuild{}, errors.New("内置执行服务摘要无效")
	}
	return build, nil
}

type workerArchive struct {
	*gzip.Reader
	file *os.File
}

func (r workerArchive) Close() error { return errors.Join(r.Reader.Close(), r.file.Close()) }
