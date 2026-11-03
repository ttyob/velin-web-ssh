import 'dart:convert';
import 'dart:typed_data';
import 'package:flutter_test/flutter_test.dart';
import 'package:velin_mobile/terminal/replay_tail.dart';

Uint8List b(String s) => Uint8List.fromList(utf8.encode(s));
String text(Uint8List d) => utf8.decode(d, allowMalformed: true);

void main() {
  test('short replay is passed through untouched', () {
    final tail = ReplayTail(limit: 100);
    tail.add(b('hello\r\n'));
    tail.add(b('world\r\n'));
    expect(text(tail.finish()), 'hello\r\nworld\r\n');
  });

  test('long replay keeps only the tail, starting on a whole line', () {
    final tail = ReplayTail(limit: 50);
    for (var i = 0; i < 20; i++) {
      tail.add(b('line number $i\r\nsecond half $i\r\n'));
    }
    expect(tail.truncated, isTrue);
    final out = text(tail.finish());
    expect(out, contains('line number 19'));
    expect(out, isNot(contains('line number 3\r')));
    expect(out.startsWith('\x1b[0m'), isTrue);
    expect(utf8.encode(out).length, lessThan(300));
  });

  test('starts at the last screen clear and re-applies skipped modes', () {
    final tail = ReplayTail(limit: 60);
    tail.add(b('\x1b[?2004h\x1b[?1000h${'old output\r\n' * 30}'));
    tail.add(b('more old\r\n' * 30));
    tail.add(b('\x1b[H\x1b[2Jfresh screen\r\nlast line'));
    final out = text(tail.finish());
    expect(out, contains('fresh screen'));
    expect(out, isNot(contains('old output')));
    expect(out, contains('\x1b[?2004h'));
    expect(out, contains('\x1b[?1000h'));
    expect(out.indexOf('\x1b[H\x1b[2J') > out.indexOf('\x1b[?2004h'), isTrue);
  });

  test('re-enters the alternate screen when the cut lands inside it', () {
    final tail = ReplayTail(limit: 40);
    tail.add(b('\x1b[?1049h${'frame\r\n' * 40}'));
    tail.add(b('frame tail\r\n' * 10));
    final out = text(tail.finish());
    expect(out, contains('\x1b[?1049h'));
    expect(out, contains('frame tail'));
  });

  test('finish resets the collector', () {
    final tail = ReplayTail(limit: 10);
    tail.add(b('x' * 50));
    tail.add(b('y' * 50));
    tail.finish();
    expect(tail.retainedBytes, 0);
    expect(tail.truncated, isFalse);
  });

  test('StreamTail keeps exactly the newest bytes across chunks', () {
    final tail = StreamTail(max: 10);
    tail.add(b('abcd'));
    tail.add(b('efgh'));
    expect(text(tail.bytes()), 'abcdefgh');
    tail.add(b('ijkl'));
    expect(text(tail.bytes()), 'cdefghijkl');
    expect(tail.size, 10);
    tail.add(b('0123456789ABCDEF'));
    expect(text(tail.bytes()), '6789ABCDEF');
    tail.clear();
    expect(tail.size, 0);
    expect(tail.bytes(), isEmpty);
  });
}
