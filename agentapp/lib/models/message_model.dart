enum MessageRole { user, assistant }

class MessageModel {
  final String nodeId;
  final String agentId;
  final MessageRole role;
  final String text;
  final int seq;

  const MessageModel({
    required this.nodeId,
    required this.agentId,
    required this.role,
    required this.text,
    required this.seq,
  });

  factory MessageModel.fromJson(Map<String, dynamic> json) => MessageModel(
        nodeId: json['nodeId'] as String? ?? '',
        agentId: json['agentId'] as String? ?? '',
        role: (json['role'] as String?) == 'user' ? MessageRole.user : MessageRole.assistant,
        text: json['text'] as String? ?? '',
        seq: (json['seq'] as num?)?.toInt() ?? 0,
      );
}
