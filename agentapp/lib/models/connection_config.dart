class ConnectionConfig {
  final String url;
  final String token;

  const ConnectionConfig({required this.url, required this.token});

  Map<String, dynamic> toJson() => {'url': url, 'token': token};

  factory ConnectionConfig.fromJson(Map<String, dynamic> json) =>
      ConnectionConfig(url: json['url'] as String, token: json['token'] as String);
}
