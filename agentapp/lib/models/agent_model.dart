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
  final bool hasHistory;
  final bool isReadOnly;
  final String readOnlyReason;
  final String attachMode;

  const AgentModel({
    required this.id,
    required this.name,
    required this.workDir,
    required this.nodeId,
    required this.provider,
    required this.status,
    this.hasHistory = false,
    this.isReadOnly = false,
    this.readOnlyReason = '',
    this.attachMode = '',
  });

  factory AgentModel.fromJson(Map<String, dynamic> json) => AgentModel(
        id: json['id'] as String,
        name: json['name'] as String? ?? '',
        workDir: json['workDir'] as String? ?? '',
        nodeId: json['nodeId'] as String? ?? '',
        provider: json['provider'] as String? ?? 'custom',
        status: _parseAgentStatus(json['status'] as String? ?? ''),
        hasHistory: json['hasHistory'] as bool? ?? false,
        isReadOnly: json['readOnly'] as bool? ?? false,
        readOnlyReason: json['readOnlyReason'] as String? ?? '',
        attachMode: json['attachMode'] as String? ?? '',
      );

  AgentModel copyWith({
    String? id,
    String? name,
    String? workDir,
    String? nodeId,
    String? provider,
    AgentStatus? status,
    bool? hasHistory,
    bool? isReadOnly,
    String? readOnlyReason,
    String? attachMode,
  }) =>
      AgentModel(
        id: id ?? this.id,
        name: name ?? this.name,
        workDir: workDir ?? this.workDir,
        nodeId: nodeId ?? this.nodeId,
        provider: provider ?? this.provider,
        status: status ?? this.status,
        hasHistory: hasHistory ?? this.hasHistory,
        isReadOnly: isReadOnly ?? this.isReadOnly,
        readOnlyReason: readOnlyReason ?? this.readOnlyReason,
        attachMode: attachMode ?? this.attachMode,
      );
}
