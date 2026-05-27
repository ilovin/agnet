enum MessageRole { user, assistant }

class MessageModel {
  final String nodeId;
  final String agentId;
  final String sessionId;
  final MessageRole role;
  final String text;
  final int seq;
  final String msgId;
  final int? timestamp;

  const MessageModel({
    required this.nodeId,
    required this.agentId,
    required this.role,
    required this.text,
    required this.seq,
    this.sessionId = '',
    this.msgId = '',
    this.timestamp,
  });

  factory MessageModel.fromJson(Map<String, dynamic> json) => MessageModel(
        nodeId: json['nodeId'] as String? ?? '',
        agentId: json['agentId'] as String? ?? '',
        sessionId: json['sessionId'] as String? ?? '',
        role: (json['role'] as String?) == 'user' ? MessageRole.user : MessageRole.assistant,
        text: json['text'] as String? ?? '',
        seq: (json['seq'] as num?)?.toInt() ?? 0,
        msgId: json['msg_id'] as String? ?? '',
        timestamp: (json['timestamp'] as num?)?.toInt(),
      );

  MessageModel copyWith({
    String? nodeId,
    String? agentId,
    String? sessionId,
    MessageRole? role,
    String? text,
    int? seq,
    String? msgId,
    int? timestamp,
  }) =>
      MessageModel(
        nodeId: nodeId ?? this.nodeId,
        agentId: agentId ?? this.agentId,
        sessionId: sessionId ?? this.sessionId,
        role: role ?? this.role,
        text: text ?? this.text,
        seq: seq ?? this.seq,
        msgId: msgId ?? this.msgId,
        timestamp: timestamp ?? this.timestamp,
      );
}
