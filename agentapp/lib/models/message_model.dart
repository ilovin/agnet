enum MessageRole { user, assistant }

class MessageModel {
  final String nodeId;
  final String agentId;
  final MessageRole role;
  final String text;
  final int seq;
  final String msgId;

  const MessageModel({
    required this.nodeId,
    required this.agentId,
    required this.role,
    required this.text,
    required this.seq,
    this.msgId = '',
  });

  factory MessageModel.fromJson(Map<String, dynamic> json) => MessageModel(
        nodeId: json['nodeId'] as String? ?? '',
        agentId: json['agentId'] as String? ?? '',
        role: (json['role'] as String?) == 'user' ? MessageRole.user : MessageRole.assistant,
        text: json['text'] as String? ?? '',
        seq: (json['seq'] as num?)?.toInt() ?? 0,
        msgId: json['msg_id'] as String? ?? '',
      );

  MessageModel copyWith({
    String? nodeId,
    String? agentId,
    MessageRole? role,
    String? text,
    int? seq,
    String? msgId,
  }) =>
      MessageModel(
        nodeId: nodeId ?? this.nodeId,
        agentId: agentId ?? this.agentId,
        role: role ?? this.role,
        text: text ?? this.text,
        seq: seq ?? this.seq,
        msgId: msgId ?? this.msgId,
      );
}
