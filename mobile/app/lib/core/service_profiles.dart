import 'dart:convert';
import 'package:shared_preferences/shared_preferences.dart';

class ServiceProfile {
  final String server, account, name;
  const ServiceProfile(this.server, this.account, {this.name = ''});
  String get encoded =>
      jsonEncode({'server': server, 'account': account, 'name': name});
}

class ServiceProfiles {
  static const key = 'mobile.profiles';
  final SharedPreferences preferences;
  ServiceProfiles(this.preferences);
  List<ServiceProfile> get all {
    final profiles = <ServiceProfile>[];
    for (final raw in preferences.getStringList(key) ?? <String>[]) {
      try {
        final j = jsonDecode(raw);
        profiles.add(
          ServiceProfile(
            j['server'] as String,
            j['account'] as String,
            name: j['name'] as String? ?? '',
          ),
        );
      } catch (_) {}
    }
    return profiles;
  }

  Future<void> remember(ServiceProfile profile) async {
    if (profile.name.isEmpty) {
      final existing = all
          .where(
            (p) => p.server == profile.server && p.account == profile.account,
          )
          .firstOrNull;
      if (existing != null) {
        profile = ServiceProfile(
          profile.server,
          profile.account,
          name: existing.name,
        );
      }
    }
    final items = all
        .where(
          (p) => p.server != profile.server || p.account != profile.account,
        )
        .toList();
    items.insert(0, profile);
    await preferences.setStringList(key, items.map((p) => p.encoded).toList());
  }

  Future<void> replace(ServiceProfile previous, ServiceProfile next) async {
    final items = all
        .where(
          (p) =>
              !(p.server == previous.server && p.account == previous.account) &&
              !(p.server == next.server && p.account == next.account),
        )
        .toList();
    items.insert(0, next);
    await preferences.setStringList(key, items.map((p) => p.encoded).toList());
  }

  Future<void> remove(ServiceProfile profile) async {
    await preferences.setStringList(
      key,
      all
          .where(
            (p) => p.server != profile.server || p.account != profile.account,
          )
          .map((p) => p.encoded)
          .toList(),
    );
    final scope = '${profile.server}|${profile.account}';
    for (final entry
        in preferences
            .getKeys()
            .where(
              (k) =>
                  k.startsWith('mobile.draft.$scope.') ||
                  k.startsWith('mobile.favorite.$scope.') ||
                  k.startsWith('mobile.recent.$scope.'),
            )
            .toList()) {
      await preferences.remove(entry);
    }
    if (preferences.getString('mobile.server') == profile.server &&
        preferences.getString('mobile.account') == profile.account) {
      await preferences.remove('mobile.server');
      await preferences.remove('mobile.account');
    }
  }
}
