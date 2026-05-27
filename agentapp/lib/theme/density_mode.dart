/// Information-density preset. Each value bundles the text and spacing
/// scale factors that the rest of the theme reads from.
enum DensityMode {
  compact(textScale: 0.92, spacingScale: 0.85, label: '紧凑'),
  standard(textScale: 1.0, spacingScale: 1.0, label: '标准'),
  comfortable(textScale: 1.08, spacingScale: 1.15, label: '宽松');

  const DensityMode({
    required this.textScale,
    required this.spacingScale,
    required this.label,
  });

  /// Multiplier applied to every TextStyle's fontSize.
  final double textScale;

  /// Multiplier applied to AppSpacing values when consumed via
  /// [scaledSpacing].
  final double spacingScale;

  /// Short Chinese label shown in the settings UI.
  final String label;
}
