# MRCP Server Plugin Architecture Design

**日期**: 2026-05-08
**状态**: 草案
**作者**: Sisyphus

---

## 1. 概述

本文档定义了在 Agent Manager (phone-talk) 项目中集成 MRCP v2 (Media Resource Control Protocol Version 2, RFC 6787) 服务器端插件的架构设计。通过该插件架构，agentd 可以作为 MRCP 资源服务器，为外部客户端（如 PBX、呼叫中心、IVR 系统）提供语音识别（ASR）和语音合成（TTS）能力。

### 1.1 核心价值

- **语音交互扩展**: 为现有的 AI Agent 管理系统增加语音通道
- **标准化接口**: 通过业界标准 MRCP v2 协议与现有通信基础设施集成
- **插件化引擎**: 支持多种 ASR/TTS 引擎的即插即用（腾讯云、百度、阿里、Google、Vosk 等）
- **复用现有架构**: 利用 agentd 的进程管理、WebSocket 事件系统和 Provider 模式

### 1.2 不在范围内

- MRCP v1 支持
- 媒体流处理（RTP 收发由独立媒体网关负责，MRCP 只负责控制面）
- 呼叫控制（SIP 信令只用于 MRCP 会话建立，不处理语音呼叫）
- 实时语音对话（WebRTC 等）

---

## 2. MRCP v2 协议概要

### 2.1 架构

```
MRCP Client (SIP UAC)          MRCP Server (SIP UAS)
├─ SIP Stack                    ├─ SIP Stack (session establishment)
├─ SDP Offer/Answer             ├─ SDP Offer/Answer
├─ TCP Connection               ├─ TCP Listener (:554 for MRCP)
│   ├─ MRCP Control Channel     │   ├─ MRCP Session Manager
│   │   ├─ Channel-ID           │   │   ├─ Resource Router
│   │   ├─ Request/Response     │   │   ├─ ASR Engine Plugin
│   │   └─ Events               │   │   └─ TTS Engine Plugin
└─ RTP Stream ─────────────────►├─ RTP Endpoint (external)
```

### 2.2 会话建立流程

1. **SIP INVITE**: Client 发送 INVITE 携带 SDP offer，包含 `m=application 9 TCP/MRCPv2` 行
2. **SDP Answer**: Server 回复 200 OK，携带 `a=channel:` 属性（格式: `<uuid>@<resource-type>`）和 TCP 端口
3. **TCP Connect**: Client 连接到 Server 提供的 TCP 端口
4. **MRCP Messages**: 通过 TCP 通道交换 MRCP 请求/响应/事件

### 2.3 资源类型

| 资源类型 | 用途 | 关键方法 |
|---------|------|---------|
| `speechsynth` | 语音合成 (TTS) | SPEAK, STOP, PAUSE, RESUME, BARGE-IN-OCCURRED |
| `speechrecog` | 语音识别 (ASR) | DEFINE-GRAMMAR, RECOGNIZE, GET-RESULT, STOP |
| `recorder` | 录音 | RECORD, STOP |
| `dtmfrecog` | DTMF 识别 | DEFINE-GRAMMAR, RECOGNIZE |

### 2.4 MRCP 消息格式

```
MRCP/2.0 <msg-length> <method> <request-id> <channel-id>
<header-name>: <header-value>
...
<empty-line>
[message-body]
```

示例 SPEAK 请求:
```
MRCP/2.0 314 SPEAK 543257 
speechsynth-00000001@speakext
Channel-Identifier: 543257@speechsynth
Content-Type: application/ssml+xml
Content-Length: 212

<?xml version="1.0"?>
<speak version="1.1">
  Hello world, this is a test of the speech synthesis engine.
</speak>
```

---

## 3. 插件架构设计

### 3.1 整体架构

```
agentd
├── WebSocket Server (:7373)          [现有]
├── AgentManager                      [现有]
└── MRCPModule (:554, configurable)
    ├── SIPAdapter                    [可选，用于SDP协商]
    ├── MRCPServer
    │   ├── TCPServer                 [MRCP 控制通道监听]
    │   ├── SessionManager            [会话生命周期管理]
    │   ├── MessageParser             [MRCP 消息编解码]
    │   └── ResourceRouter            [请求分发到引擎]
    ├── PluginRegistry
    │   ├── ASR Plugins               [speechrecog 引擎集合]
    │   │   ├── tencent-asr
    │   │   ├── baidu-asr
    │   │   └── vosk-asr
    │   └── TTS Plugins               [speechsynth 引擎集合]
    │       ├── tencent-tts
    │       ├── aliyun-tts
    │       └── google-tts
    └── MediaBridge                   [RTP 媒体桥接，预留接口]
```

### 3.2 核心接口

#### 3.2.1 Engine Plugin 接口

```go
package mrcp

// EngineType 标识引擎类型
type EngineType string

const (
	EngineTypeASR EngineType = "speechrecog"
	EngineTypeTTS EngineType = "speechsynth"
	EngineTypeRec EngineType = "recorder"
)

// Engine 是所有 MRCP 引擎插件必须实现的接口
type Engine interface {
	// Type 返回引擎支持的资源类型
	Type() EngineType
	
	// Name 返回引擎唯一标识名（如 "tencent-asr", "vosk-local"）
	Name() string
	
	// Init 初始化引擎，传入配置
	Init(config map[string]interface{}) error
	
	// Close 释放引擎资源
	Close() error
}

// ASREngine 语音识别引擎接口
type ASREngine interface {
	Engine
	
	// DefineGrammar 加载语法（支持 SRGS、JSGF）
	DefineGrammar(ctx context.Context, grammarID string, grammarBody []byte, contentType string) error
	
	// Recognize 开始识别
	// 参数：audioStream 为 RTP 音频流读取器，hints 为识别参数
	// 返回：识别结果通道和错误
	Recognize(ctx context.Context, audioStream io.Reader, hints *RecognitionHints) (<-chan RecognitionEvent, error)
	
	// StopRecognition 停止当前识别
	StopRecognition(ctx context.Context) error
}

// TTSEngine 语音合成引擎接口
type TTSEngine interface {
	Engine
	
	// Speak 开始合成语音
	// 参数：text 为待合成文本（纯文本或 SSML），voice 为发音人参数
	// 返回：音频数据通道和错误
	Speak(ctx context.Context, text string, voice *VoiceParameters) (<-chan AudioEvent, error)
	
	// StopSpeaking 停止当前合成
	StopSpeaking(ctx context.Context) error
	
	// Pause/Resume 暂停和恢复
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error
}

// RecognitionHints 识别参数
type RecognitionHints struct {
	Language        string
	ConfidenceThreshold float64
	NoInputTimeout  time.Duration
	RecognitionTimeout time.Duration
	SpeechCompleteTimeout time.Duration
	SpeechIncompleteTimeout time.Duration
	DTMFTerminator  string
	Hotwords        []string
}

// VoiceParameters 合成参数
type VoiceParameters struct {
	Language    string
	Speaker     string
	Speed       float64  // 语速 0.5-2.0
	Pitch       float64  // 音调 0.5-2.0
	Volume      float64  // 音量 0.0-1.0
	SampleRate  int      // 采样率 8000/16000
}

// RecognitionEvent 识别事件
type RecognitionEvent struct {
	Type        string  // "partial-result", "final-result", "start-of-speech", "no-input-timeout", "recognition-timeout"
	Result      string  // 识别文本
	Confidence  float64 // 置信度 0.0-1.0
	GrammarURI  string
	WaveformURI string  // 录音文件URI
}

// AudioEvent 音频事件
type AudioEvent struct {
	Type      string // "audio", "complete", "failed"
	Data      []byte // PCM/MP3 音频数据
	Duration  time.Duration
}
```

#### 3.2.2 Plugin Registry

```go
// PluginRegistry 管理所有已注册的 MRCP 引擎插件
type PluginRegistry struct {
	mu     sync.RWMutex
	asr    map[string]ASREngine
	tts    map[string]TTSEngine
}

func (r *PluginRegistry) RegisterASR(engine ASREngine) error
func (r *PluginRegistry) RegisterTTS(engine TTSEngine) error
func (r *PluginRegistry) Unregister(name string) error
func (r *PluginRegistry) GetASR(name string) (ASREngine, bool)
func (r *PluginRegistry) GetTTS(name string) (TTSEngine, bool)
func (r *PluginRegistry) ListASR() []string
func (r *PluginRegistry) ListTTS() []string
```

### 3.3 服务器核心组件

#### 3.3.1 MRCPServer

```go
// Server 配置
type ServerConfig struct {
	ListenAddr      string        // MRCP TCP 监听地址，默认 ":554"
	SIPAddr         string        // SIP UDP 监听地址，默认 ":5060"（可选）
	DefaultASR      string        // 默认 ASR 引擎名
	DefaultTTS      string        // 默认 TTS 引擎名
	MaxSessions     int           // 最大并发会话数
	SessionTimeout  time.Duration // 会话超时时间
}

// Server MRCP 服务器
type Server struct {
	config    *ServerConfig
	registry  *PluginRegistry
	sessions  *SessionManager
	listener  net.Listener
	mu        sync.RWMutex
	running   bool
}

func NewServer(config *ServerConfig, registry *PluginRegistry) *Server
func (s *Server) Start() error
func (s *Server) Stop() error
```

#### 3.3.2 SessionManager

```go
// Session 表示一个 MRCP 资源会话
type Session struct {
	ChannelID     string        // 格式: <uuid>@<resource-type>
	ResourceType  EngineType
	EngineName    string        // 实际使用的引擎名
	Engine        Engine        // 引擎实例
	State         SessionState  // idle, recognizing, speaking, recording
	CreatedAt     time.Time
	LastActivity  time.Time
	mu            sync.Mutex
}

type SessionState string

const (
	SessionStateIdle         SessionState = "idle"
	SessionStateRecognizing  SessionState = "recognizing"
	SessionStateSpeaking     SessionState = "speaking"
	SessionStateRecording    SessionState = "recording"
)

type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session // key: channel-id
	config   *ServerConfig
}

func (m *SessionManager) Create(channelID string, resourceType EngineType, engineName string, engine Engine) (*Session, error)
func (m *SessionManager) Get(channelID string) (*Session, bool)
func (m *SessionManager) Remove(channelID string) error
func (m *SessionManager) CleanupExpired()
```

#### 3.3.3 MessageHandler

```go
// MessageHandler 处理 MRCP 请求并生成响应
type MessageHandler struct {
	registry *PluginRegistry
	sessions *SessionManager
}

func (h *MessageHandler) HandleRequest(msg *MRCPMessage) (*MRCPMessage, error)

// 核心处理方法
func (h *MessageHandler) handleDEFINEGRAMMAR(req *MRCPMessage, sess *Session) (*MRCPMessage, error)
func (h *MessageHandler) handleRECOGNIZE(req *MRCPMessage, sess *Session) (*MRCPMessage, error)
func (h *MessageHandler) handleGETRESULT(req *MRCPMessage, sess *Session) (*MRCPMessage, error)
func (h *MessageHandler) handleSPEAK(req *MRCPMessage, sess *Session) (*MRCPMessage, error)
func (h *MessageHandler) handleSTOP(req *MRCPMessage, sess *Session) (*MRCPMessage, error)
func (h *MessageHandler) handlePAUSE(req *MRCPMessage, sess *Session) (*MRCPMessage, error)
func (h *MessageHandler) handleRESUME(req *MRCPMessage, sess *Session) (*MRCPMessage, error)
```

### 3.4 消息结构

```go
// MRCPMessage MRCP v2 协议消息
type MRCPMessage struct {
	Version         string            // "2.0"
	MessageLength   int               // 消息总长度
	MessageType     MRCPMessageType   // request, response, event
	Method          string            // SPEAK, RECOGNIZE, STOP, etc.
	RequestID       int               // 请求ID
	ChannelID       string            // 通道标识
	StatusCode      int               // 响应状态码
	RequestState    RequestState      // complete, pending
	Headers         map[string]string // MRCP 头部
	Body            []byte            // 消息体
	BodyContentType string            // Content-Type
}

type MRCPMessageType string

const (
	MessageTypeRequest  MRCPMessageType = "request"
	MessageTypeResponse MRCPMessageType = "response"
	MessageTypeEvent    MRCPMessageType = "event"
)

type RequestState string

const (
	RequestStateComplete RequestState = "complete"
	RequestStatePending  RequestState = "pending"
)
```

---

## 4. 与现有架构集成

### 4.1 agentd 集成点

```
agentd/main.go
└── 新增: mrcpServer 初始化和启动

agentd/internal/config/config.go
└── 新增: MRCP 配置段

type MRCPConfig struct {
    Enabled     bool              `yaml:"enabled"`
    ListenAddr  string            `yaml:"listen_addr"`
    DefaultASR  string            `yaml:"default_asr"`
    DefaultTTS  string            `yaml:"default_tts"`
    Engines     []EngineConfig    `yaml:"engines"`
}

type EngineConfig struct {
    Name   string                 `yaml:"name"`
    Type   string                 `yaml:"type"`   // "asr" or "tts"
    Driver string                 `yaml:"driver"` // "tencent", "baidu", "vosk", etc.
    Config map[string]interface{} `yaml:"config"`
}
```

### 4.2 WebSocket 事件集成

MRCP 服务器产生的关键事件通过现有 EventBuffer 机制推送给 App:

```go
// 新增事件类型
const (
    EventMRCPServiceStarted   = "mrcp.service.started"
    EventMRCPServiceStopped   = "mrcp.service.stopped"
    EventMRCPSessionCreated   = "mrcp.session.created"
    EventMRCPSessionDestroyed = "mrcp.session.destroyed"
    EventMRCPRecognitionResult = "mrcp.recognition.result"
    EventMRCPSpeakComplete    = "mrcp.speak.complete"
)
```

### 4.3 Provider 模式复用

MRCP 引擎插件复用现有 Provider 概念:

```
agentd/internal/agent/provider.go      [现有: AI Agent Provider]
agentd/internal/mrcp/plugin.go          [新增: MRCP Engine Plugin]
```

两者都是"启动外部进程/服务并与之交互"的抽象，但职责不同:
- **Agent Provider**: 管理 AI 编码 Agent 的生命周期
- **MRCP Plugin**: 管理语音引擎实例和语音处理请求

---

## 5. 插件实现示例

### 5.1 Vosk ASR 插件（本地离线识别）

```go
package vosk

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "os/exec"
    "time"
    
    "github.com/phone-talk/agentd/internal/mrcp"
)

// VoskASR 使用本地 Vosk 服务进行离线识别
type VoskASR struct {
    modelPath string
    sampleRate int
    lang      string
    cmd       *exec.Cmd
}

func (v *VoskASR) Type() mrcp.EngineType { return mrcp.EngineTypeASR }
func (v *VoskASR) Name() string          { return "vosk-asr" }

func (v *VoskASR) Init(config map[string]interface{}) error {
    v.modelPath = config["model_path"].(string)
    v.sampleRate = config["sample_rate"].(int)
    v.lang = config["language"].(string)
    return nil
}

func (v *VoskASR) Close() error {
    if v.cmd != nil && v.cmd.Process != nil {
        return v.cmd.Process.Kill()
    }
    return nil
}

func (v *VoskASR) DefineGrammar(ctx context.Context, grammarID string, grammarBody []byte, contentType string) error {
    // Vosk 支持通过 speaker model 或 graph 定制，简化实现可返回不支持
    return fmt.Errorf("grammar definition not supported by vosk")
}

func (v *VoskASR) Recognize(ctx context.Context, audioStream io.Reader, hints *mrcp.RecognitionHints) (<-chan mrcp.RecognitionEvent, error) {
    events := make(chan mrcp.RecognitionEvent, 10)
    
    go func() {
        defer close(events)
        // 启动 vosk 识别进程或调用其 API
        // 读取 audioStream，分块送入识别引擎
        // 输出 partial-result 和 final-result 事件
    }()
    
    return events, nil
}

func (v *VoskASR) StopRecognition(ctx context.Context) error {
    // 发送停止信号
    return nil
}
```

### 5.2 腾讯云 ASR/TTS 插件

```go
package tencent

import (
    "context"
    "fmt"
    
    "github.com/phone-talk/agentd/internal/mrcp"
    tencentasr "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/asr"
    tencenttts "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/tts"
)

// TencentASR 腾讯云语音识别
type TencentASR struct {
    secretID  string
    secretKey string
    region    string
}

func (t *TencentASR) Type() mrcp.EngineType { return mrcp.EngineTypeASR }
func (t *TencentASR) Name() string          { return "tencent-asr" }

func (t *TencentASR) Init(config map[string]interface{}) error {
    t.secretID = config["secret_id"].(string)
    t.secretKey = config["secret_key"].(string)
    t.region = config["region"].(string)
    return nil
}

// ... 实现 ASREngine 接口

// TencentTTS 腾讯云语音合成
type TencentTTS struct {
    secretID  string
    secretKey string
    region    string
}

func (t *TencentTTS) Type() mrcp.EngineType { return mrcp.EngineTypeTTS }
func (t *TencentTTS) Name() string          { return "tencent-tts" }

// ... 实现 TTSEngine 接口
```

---

## 6. 生命周期管理

### 6.1 服务器启动流程

```
1. agentd 读取配置文件
2. 初始化 PluginRegistry
3. 根据配置加载引擎插件（Init）
4. 创建 MRCPServer 实例
5. 启动 TCP 监听器
6. [可选] 启动 SIP UDP 监听器
7. 注册 WebSocket 事件处理器
```

### 6.2 会话生命周期

```
SIP INVITE (with SDP offer)
    ↓
SIP 200 OK (with SDP answer, channel-id)
    ↓
Client TCP Connect → Server accept
    ↓
MRCP SETUP (或直接使用已协商的通道)
    ↓
Session Created (分配引擎实例)
    ↓
MRCP RECOGNIZE / SPEAK / ...
    ↓
MRCP Events (START-OF-SPEECH, RECOGNITION-COMPLETE, SPEAK-COMPLETE)
    ↓
SIP BYE / TCP Disconnect
    ↓
Session Destroyed (释放引擎资源)
```

### 6.3 错误处理

| 场景 | MRCP 状态码 | 处理方式 |
|------|------------|---------|
| 引擎未加载 | 407 | 返回失败，记录日志 |
| 会话不存在 | 481 | 返回失败 |
| 引擎调用失败 | 407 | 返回失败，尝试切换备用引擎 |
| 识别超时 | 无 | 发送 RECOGNITION-TIMEOUT 事件 |
| 无输入超时 | 无 | 发送 NO-INPUT-TIMEOUT 事件 |

---

## 7. 目录结构

```
agentd/internal/mrcp/
├── doc.go              # 包文档
├── server.go           # MRCPServer 主结构
├── server_test.go      # 服务器测试
├── session.go          # Session 和 SessionManager
├── session_test.go     # 会话管理测试
├── message.go          # MRCPMessage 结构和解码
├── message_test.go     # 消息编解码测试
├── handler.go          # 请求处理器
├── handler_test.go     # 处理器测试
├── plugin.go           # 插件接口定义
├── registry.go         # PluginRegistry
├── registry_test.go    # 注册表测试
├── events.go           # 事件类型定义
├── config.go           # MRCP 配置结构
├── codecs/             # 音频编解码工具
│   └── pcm.go
├── plugins/            # 内置插件实现
│   ├── mock/           # 测试用 mock 插件
│   │   ├── mock_asr.go
│   │   └── mock_tts.go
│   └── vosk/           # Vosk 本地识别（示例）
│       └── vosk_asr.go
└── integration_test.go # 集成测试
```

---

## 8. 测试策略

### 8.1 单元测试

- `message_test.go`: MRCP 消息解析和序列化（覆盖 RFC 6787 所有消息类型）
- `session_test.go`: 会话创建、查询、销毁、过期清理
- `registry_test.go`: 插件注册、注销、重复注册、并发访问
- `handler_test.go`: 各 MRCP 方法处理逻辑（使用 mock 引擎）

### 8.2 集成测试

- `integration_test.go`: 完整 MRCP 会话流程（SETUP → SPEAK → STOP → TEARDOWN）
- 使用 `net.Pipe()` 或本地 TCP 连接模拟客户端

### 8.3 测试用 Mock 插件

```go
package mock

// MockASR 返回预设结果的 ASR 引擎
type MockASR struct {
    PredefinedResult string
    Delay            time.Duration
}

func (m *MockASR) Recognize(...) (<-chan mrcp.RecognitionEvent, error) {
    events := make(chan mrcp.RecognitionEvent, 1)
    go func() {
        time.Sleep(m.Delay)
        events <- mrcp.RecognitionEvent{
            Type:     "final-result",
            Result:   m.PredefinedResult,
            Confidence: 0.95,
        }
        close(events)
    }()
    return events, nil
}
```

---

## 9. 关键设计决策

1. **独立模块**: MRCP 服务器作为 `agentd/internal/mrcp/` 独立包，不侵入现有 agent/ws 代码
2. **可选启动**: 通过配置 `mrcp.enabled` 控制是否启动 MRCP 服务，默认关闭
3. **TCP 直连优先**: 第一版实现仅支持 TCP 直连模式（Client 已知 MRCP 地址），SIP 信令作为第二版扩展
4. **引擎无状态**: 引擎实例本身无状态，状态维护在 Session 层，便于水平扩展
5. **RTP 外部化**: MRCP 只处理控制面，RTP 媒体流通过外部媒体网关或直连方式处理
6. **复用 EventBuffer**: MRCP 事件通过现有 EventBuffer 推送到 App，保持架构一致性
7. **Provider 模式启发**: 借鉴现有 Agent Provider 的接口设计，但保持独立（语音引擎 ≠ AI Agent）

---

## 10. 实现优先级

1. **P0 - 核心框架**: Plugin 接口、Registry、Message 编解码、Session 管理
2. **P1 - 服务器**: TCPServer、MessageHandler、基础请求处理 (SPEAK, RECOGNIZE, STOP)
3. **P2 - Mock 插件**: 用于测试的 Mock ASR/TTS
4. **P3 - 真实插件**: Vosk ASR（本地免费）、腾讯云 ASR/TTS（云端）
5. **P4 - SIP 支持**: SDP 协商、SIP INVITE 处理
6. **P5 - App 集成**: WebSocket 事件、仪表盘 MRCP 状态展示

---

## 11. 参考资源

- [RFC 6787 - MRCPv2](https://tools.ietf.org/html/rfc6787)
- [go-mrcp](https://github.com/hateeyan/go-mrcp) - Pure Go MRCPv2 library
- [UniMRCP](https://www.unimrcp.org/) - C 语言 MRCP 参考实现
- [Vosk](https://github.com/alphacep/vosk-api) - 离线语音识别引擎
