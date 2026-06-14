package main

import (
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultServerPort = "8080"
	friendlyName      = "Home Cinema"
	manufacturerName  = "Home Cinema"
	modelName         = "HomeCinemaStreamer"
	appVersion        = "1.8.1"
	logFileName       = "server.log"
	browseCacheTTL    = 5 * time.Second
	burstAliveCount   = 5
	progressFileName  = "progress.json"
)

// uuid is set in main() via loadOrCreateUUID; persisted in dataDir/server_uuid.
var uuid = "673f-431d-90b6-homecinema-001"

var (
	// callbackRe парсит UPnP CALLBACK-заголовок subscribe-запроса: формат
	// <http://host/path> [<http://host2/path2>]. SOAP-тело Browse-запросов
	// разбирается через extractXMLTag — strings.Index быстрее regexp.
	callbackRe = regexp.MustCompile(`<([^>]+)>`)
)

var (
	serverPort      = defaultServerPort
	startedAt       = time.Now()
	defaultMediaDir = resolveDefaultMediaDir()
	currentMediaDir = defaultMediaDir
	defaultDataDir  = resolveDefaultDataDir()
	dataDir         = defaultDataDir
	logFilePath     = filepath.Join(dataDir, logFileName)
	progressFile    = filepath.Join(dataDir, progressFileName)
	remoteControlOK = false
)

var (
	mediaDirMu    sync.RWMutex
	browseCache   = make(map[string]browseCacheEntry)
	browseCacheMu sync.RWMutex
)

var (
	streamDebugEnabled bool
	streamDebugHeaders bool
	streamDebugEvery   = 15 * time.Second
	streamSlowRead     = 200 * time.Millisecond
	streamSlowWrite    = 200 * time.Millisecond
)

var (
	streamSeq            uint64
	activeStreamRequests int64
)

var browseUpdateID uint32

var (
	tvStreamEnabled  = true
	tvStreamFirst    = false
	tvAutoFirst      = true
	tvAutoFirstMbps  = 8
	tvVideoCRF       = 22
	tvVideoMaxrateMb = 10
	tvVideoBufsizeMb = 40
	tvVideoPreset    = "veryfast"
	tvAudioKbps      = 384
	tvAudioChannels  = 6
	tvContentType    = "video/mpeg"
	tvDLNAFeatures   = "DLNA.ORG_PN=AVC_TS_HD_24_AC3_ISO;DLNA.ORG_OP=10;DLNA.ORG_CI=1;DLNA.ORG_FLAGS=01700000000000000000000000000000"
)

var (
	warmupMetaEnabled  = true
	warmupMetaThrottle time.Duration
	warmupMetaMaxFiles int
)

func bumpBrowseUpdateID() uint32 {
	return atomic.AddUint32(&browseUpdateID, 1)
}

func currentBrowseUpdateID() uint32 {
	return atomic.LoadUint32(&browseUpdateID)
}
