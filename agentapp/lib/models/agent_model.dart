enum AgentStatus { starting, idle, working, stopped, crashed }

AgentStatus _parseAgentStatus(String s) {
  switch (s) {
    case 'starting':
      return AgentStatus.starting;
    case 'working':
      return AgentStatus.working;
    case 'stopped':
      return AgentStatus.stopped;
    case 'crashed':
      return AgentStatus.crashed;
    default:
      return AgentStatus.idle;
  }
}

class AgentModel {
  final String id;
  final String name;
  final String workDir;
  final String nodeId;
  final String provider;
  final AgentStatus status;
  final int? pid;
  final bool hasHistory;
  final bool isReadOnly;
  final String readOnlyReason;
  final String attachMode;
  final String? projectName;
  final String? sessionId;
  final String? runtimeState;
  final String? sessionState;
  final String? sessionStateReason;
  final String? sessionControl;
  final String? providerState;
  final String? providerScope;
  final String? providerWriteMode;
  final String? providerReadOnlyReason;
  final String? permissionMode;

  const AgentModel({
    required this.id,
    required this.name,
    required this.workDir,
    required this.nodeId,
    required this.provider,
    required this.status,
    this.pid,
    this.hasHistory = false,
    this.isReadOnly = false,
    this.readOnlyReason = '',
    this.attachMode = '',
    this.projectName,
    this.sessionId,
    this.runtimeState,
    this.sessionState,
    this.sessionStateReason,
    this.sessionControl,
    this.providerState,
    this.providerScope,
    this.providerWriteMode,
    this.providerReadOnlyReason,
    this.permissionMode,
  });

  factory AgentModel.fromJson(Map<String, dynamic> json) => AgentModel(
    id: json['id'] as String,
    name: json['name'] as String? ?? '',
    workDir: json['workDir'] as String? ?? '',
    nodeId: json['nodeId'] as String? ?? '',
    provider: json['provider'] as String? ?? 'custom',
    status: _parseAgentStatus(json['status'] as String? ?? ''),
    pid: (json['pid'] as num?)?.toInt(),
    hasHistory: json['hasHistory'] as bool? ?? false,
    isReadOnly: json['readOnly'] as bool? ?? false,
    readOnlyReason: json['readOnlyReason'] as String? ?? '',
    attachMode: json['attachMode'] as String? ?? '',
    projectName: json['projectName'] as String?,
    sessionId: json['sessionId'] as String?,
    runtimeState: json['runtimeState'] as String?,
    sessionState: json['sessionState'] as String?,
    sessionStateReason: json['sessionStateReason'] as String?,
    sessionControl: json['sessionControl'] as String?,
    providerState: json['providerState'] as String?,
    providerScope: json['providerScope'] as String?,
    providerWriteMode: json['providerWriteMode'] as String?,
    providerReadOnlyReason: json['providerReadOnlyReason'] as String?,
    permissionMode: json['permissionMode'] as String?,
  );

  AgentModel copyWith({
    String? id,
    String? name,
    String? workDir,
    String? nodeId,
    String? provider,
    AgentStatus? status,
    int? pid,
    bool? hasHistory,
    bool? isReadOnly,
    String? readOnlyReason,
    String? attachMode,
    String? projectName,
    String? sessionId,
    String? runtimeState,
    String? sessionState,
    String? sessionStateReason,
    String? sessionControl,
    String? providerState,
    String? providerScope,
    String? providerWriteMode,
    String? providerReadOnlyReason,
    String? permissionMode,
  }) => AgentModel(
    id: id ?? this.id,
    name: name ?? this.name,
    workDir: workDir ?? this.workDir,
    nodeId: nodeId ?? this.nodeId,
    provider: provider ?? this.provider,
    status: status ?? this.status,
    pid: pid ?? this.pid,
    hasHistory: hasHistory ?? this.hasHistory,
    isReadOnly: isReadOnly ?? this.isReadOnly,
    readOnlyReason: readOnlyReason ?? this.readOnlyReason,
    attachMode: attachMode ?? this.attachMode,
    projectName: projectName ?? this.projectName,
    sessionId: sessionId ?? this.sessionId,
    runtimeState: runtimeState ?? this.runtimeState,
    sessionState: sessionState ?? this.sessionState,
    sessionStateReason: sessionStateReason ?? this.sessionStateReason,
    sessionControl: sessionControl ?? this.sessionControl,
    providerState: providerState ?? this.providerState,
    providerScope: providerScope ?? this.providerScope,
    providerWriteMode: providerWriteMode ?? this.providerWriteMode,
    providerReadOnlyReason:
        providerReadOnlyReason ?? this.providerReadOnlyReason,
    permissionMode: permissionMode ?? this.permissionMode,
  );
}
