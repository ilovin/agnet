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

/// Location information for a node (local or remote)
class NodeLocation {
  final String type; // 'local' or 'remote'
  final String host;
  final String displayLocation;

  const NodeLocation({
    required this.type,
    required this.host,
    required this.displayLocation,
  });

  factory NodeLocation.fromJson(Map<String, dynamic> json) => NodeLocation(
        type: json['type'] as String? ?? 'remote',
        host: json['host'] as String? ?? '',
        displayLocation: json['displayLocation'] as String? ?? '',
      );

  bool get isLocal => type == 'local';

  Map<String, dynamic> toJson() => {
        'type': type,
        'host': host,
        'displayLocation': displayLocation,
      };
}

class NodeModel {
  final String id;
  final String name;
  final String host;
  final NodeStatus status;
  final NodeLocation location;

  const NodeModel({
    required this.id,
    required this.name,
    required this.host,
    required this.status,
    required this.location,
  });

  bool get isLocal => location.isLocal;

  factory NodeModel.fromJson(Map<String, dynamic> json) {
    final locationJson = json['location'] as Map<String, dynamic>?;
    final host = json['host'] as String? ?? '';
    return NodeModel(
      id: json['id'] as String,
      name: json['name'] as String? ?? '',
      host: host,
      status: _parseNodeStatus(json['status'] as String? ?? ''),
      location: locationJson != null
          ? NodeLocation.fromJson(locationJson)
          : NodeLocation(
              type: _isLocalHost(host) ? 'local' : 'remote',
              host: host,
              displayLocation: _isLocalHost(host) ? 'localhost' : host,
            ),
    );
  }

  NodeModel copyWith({
    String? id,
    String? name,
    String? host,
    NodeStatus? status,
    NodeLocation? location,
  }) =>
      NodeModel(
        id: id ?? this.id,
        name: name ?? this.name,
        host: host ?? this.host,
        status: status ?? this.status,
        location: location ?? this.location,
      );
}

bool _isLocalHost(String host) {
  return host == 'localhost' || host == '127.0.0.1' || host == '::1';
}
