import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:agentapp/providers/density_mode_provider.dart';
import 'package:agentapp/theme/density_mode.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  test('default density is standard', () async {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    expect(container.read(densityModeProvider), DensityMode.standard);
  });

  test('hydrates from SharedPreferences when present', () async {
    SharedPreferences.setMockInitialValues({
      'density_mode': DensityMode.comfortable.index,
    });
    final container = ProviderContainer();
    addTearDown(container.dispose);
    // Force notifier construction so that its async _load() actually starts.
    container.read(densityModeProvider);
    // Yield to the event loop a few times to let _load() resolve.
    for (var i = 0; i < 10; i++) {
      await Future<void>.delayed(Duration.zero);
    }
    expect(container.read(densityModeProvider), DensityMode.comfortable);
  });

  test('set() updates state and persists to SharedPreferences', () async {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    await container
        .read(densityModeProvider.notifier)
        .set(DensityMode.compact);
    expect(container.read(densityModeProvider), DensityMode.compact);

    final prefs = await SharedPreferences.getInstance();
    expect(prefs.getInt('density_mode'), DensityMode.compact.index);
  });

  test('clamps invalid stored index back into range', () async {
    SharedPreferences.setMockInitialValues({'density_mode': 99});
    final container = ProviderContainer();
    addTearDown(container.dispose);
    container.read(densityModeProvider);
    for (var i = 0; i < 10; i++) {
      await Future<void>.delayed(Duration.zero);
    }
    final mode = container.read(densityModeProvider);
    expect(DensityMode.values, contains(mode));
  });
}
