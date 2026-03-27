enum NodeStatus { connected, disconnected, connecting, deploying, error }

NodeStatus _parseNodeStatus(String s) {
  switch (s) {
    case 'connected':
      return NodeStatus.connected;
    case 'connecting':
      return NodeStatus.connecting;
    case 'deploying':
      return NodeStatus.deploying;
    case 'error':
      return NodeStatus.error;
    default:
      return NodeStatus.disconnected;
  }
}

class NodeModel {
  final String id;
  final String name;
  final String host;
  final NodeStatus status;

  const NodeModel({
    required this.id,
    required this.name,
    required this.host,
    required this.status,
  });

  factory NodeModel.fromJson(Map<String, dynamic> json) => NodeModel(
        id: json['id'] as String,
        name: json['name'] as String? ?? '',
        host: json['host'] as String? ?? '',
        status: _parseNodeStatus(json['status'] as String? ?? ''),
      );

  NodeModel copyWith({String? id, String? name, String? host, NodeStatus? status}) => NodeModel(
        id: id ?? this.id,
        name: name ?? this.name,
        host: host ?? this.host,
        status: status ?? this.status,
      );
}
