import 'package:flutter/material.dart';
import '../models/agent_model.dart';

/// 统一的状态颜色主题
/// 用于Dashboard和AgentDetail等所有显示Agent状态颜色的地方
class AgentStatusTheme {
  static Color getColor(AgentStatus status) {
    switch (status) {
      case AgentStatus.working:
        return Colors.blue;
      case AgentStatus.idle:
        return Colors.green;
      case AgentStatus.starting:
        return Colors.orange;
      case AgentStatus.stopped:
        return Colors.grey;
      case AgentStatus.crashed:
        return Colors.red;
    }
  }

  static String getLabel(AgentStatus status) {
    switch (status) {
      case AgentStatus.working:
        return 'Working';
      case AgentStatus.idle:
        return 'Standby';
      case AgentStatus.starting:
        return 'Starting…';
      case AgentStatus.stopped:
        return 'Stopped';
      case AgentStatus.crashed:
        return 'Crashed';
    }
  }
}
