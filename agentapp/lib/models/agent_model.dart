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

  const AgentModel({
    required this.id,
    required this.name,
    required this.workDir,
    required this.nodeId,
    required this.provider,
    required this.status,
  });

  factory AgentModel.fromJson(Map<String, dynamic> json) => AgentModel(
        id: json['id'] as String,
        name: json['name'] as String? ?? '',
        workDir: json['workDir'] as String? ?? '',
        nodeId: json['nodeId'] as String? ?? '',
        provider: json['provider'] as String? ?? 'custom',
        status: _parseAgentStatus(json['status'] as String? ?? ''),
      );

  AgentModel copyWith({
    String? id,
    String? name,
    String? workDir,
    String? nodeId,
    String? provider,
    AgentStatus? status,
  }) =>
      AgentModel(
        id: id ?? this.id,
        name: name ?? this.name,
        workDir: workDir ?? this.workDir,
        nodeId: nodeId ?? this.nodeId,
        provider: provider ?? this.provider,
        status: status ?? this.status,
      );
}
