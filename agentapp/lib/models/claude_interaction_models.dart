// Protocol models for R-010: App ↔ agentd interaction events.
//
// These plain Dart classes mirror the Go structs defined in
// agentd/internal/agent/event_kinds.go.  They are intentionally
// simple (no freezed, no code-gen) to stay consistent with the
// existing hand-written model style in this project.
//
// Parsing is done via named factory constructors that accept a
// raw Map<String, dynamic> and are null-safe / backward-compatible:
// unknown or missing fields always produce a sensible default so
// that messages emitted by an older agentd version are never fatal.

// ---------------------------------------------------------------------------
// AskUserQuestion event  (kind == 'ask_user_question')
// ---------------------------------------------------------------------------

/// A selectable choice inside an [AskUserQuestion].
class AskUserQuestionOption {
  final String label;
  final String description;
  final String preview;

  const AskUserQuestionOption({
    required this.label,
    this.description = '',
    this.preview = '',
  });

  factory AskUserQuestionOption.fromJson(Map<String, dynamic> json) =>
      AskUserQuestionOption(
        label: json['label'] as String? ?? '',
        description: json['description'] as String? ?? '',
        preview: json['preview'] as String? ?? '',
      );

  Map<String, dynamic> toJson() => {
        'label': label,
        if (description.isNotEmpty) 'description': description,
        if (preview.isNotEmpty) 'preview': preview,
      };
}

/// A single question within an [AskUserQuestionPayload].
class AskUserQuestion {
  final String question;
  final String header;
  final bool multiSelect;
  final List<AskUserQuestionOption> options;

  const AskUserQuestion({
    required this.question,
    this.header = '',
    this.multiSelect = false,
    this.options = const [],
  });

  factory AskUserQuestion.fromJson(Map<String, dynamic> json) =>
      AskUserQuestion(
        question: json['question'] as String? ?? '',
        header: json['header'] as String? ?? '',
        multiSelect: json['multi_select'] as bool? ?? false,
        options: (json['options'] as List<dynamic>?)
                ?.map((e) => AskUserQuestionOption.fromJson(
                    Map<String, dynamic>.from(e as Map)))
                .toList() ??
            const [],
      );

  Map<String, dynamic> toJson() => {
        'question': question,
        if (header.isNotEmpty) 'header': header,
        'multi_select': multiSelect,
        'options': options.map((o) => o.toJson()).toList(),
      };
}

/// Payload for conversation events with kind == 'ask_user_question'.
///
/// The payload is transmitted inside the event's top-level map under the
/// key 'askUserQuestion' (parallel to how 'permissionRequest' is nested
/// for permission_request events).
class AskUserQuestionPayload {
  final String toolUseId;
  final List<AskUserQuestion> questions;

  const AskUserQuestionPayload({
    required this.toolUseId,
    required this.questions,
  });

  factory AskUserQuestionPayload.fromJson(Map<String, dynamic> json) =>
      AskUserQuestionPayload(
        toolUseId: json['tool_use_id'] as String? ?? '',
        questions: (json['questions'] as List<dynamic>?)
                ?.map((e) => AskUserQuestion.fromJson(
                    Map<String, dynamic>.from(e as Map)))
                .toList() ??
            const [],
      );

  Map<String, dynamic> toJson() => {
        'tool_use_id': toolUseId,
        'questions': questions.map((q) => q.toJson()).toList(),
      };
}

// ---------------------------------------------------------------------------
// ExitPlanMode event  (kind == 'exit_plan_mode')
// ---------------------------------------------------------------------------

/// Payload for conversation events with kind == 'exit_plan_mode'.
///
/// The payload is transmitted inside the event's top-level map under the
/// key 'exitPlanMode'.
class ExitPlanModePayload {
  final String toolUseId;
  final String plan;

  const ExitPlanModePayload({
    required this.toolUseId,
    required this.plan,
  });

  factory ExitPlanModePayload.fromJson(Map<String, dynamic> json) =>
      ExitPlanModePayload(
        toolUseId: json['tool_use_id'] as String? ?? '',
        plan: json['plan'] as String? ?? '',
      );

  Map<String, dynamic> toJson() => {
        'tool_use_id': toolUseId,
        'plan': plan,
      };
}

// ---------------------------------------------------------------------------
// Kind constants (matches agentd/internal/agent/event_kinds.go)
// ---------------------------------------------------------------------------

/// Event kind string constants for conversation events.
///
/// These values appear in the 'kind' field of JSON-RPC conversation.message
/// events pushed by agentd over the WebSocket.
class ConversationEventKind {
  ConversationEventKind._();

  static const String toolUse = 'tool_use';
  static const String toolResult = 'tool_result';
  static const String permissionRequest = 'permission_request';
  static const String permissionPrompt = 'permission_prompt';
  static const String askUserQuestion = 'ask_user_question';
  static const String exitPlanMode = 'exit_plan_mode';
}
