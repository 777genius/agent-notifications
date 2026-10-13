#!/usr/bin/python3
"""Pure native wire contract; no Gio, display, notification or process launch."""
import importlib.util
from pathlib import Path
import unittest

path = Path(__file__).with_name('navigation-linux-wayland-token-probe.py')
spec = importlib.util.spec_from_file_location('native_TEST_protocol', path)
protocol = importlib.util.module_from_spec(spec)
spec.loader.exec_module(protocol)
NONCE = '0123456789abcdef0123456789abcdef'
APP = 'org.notification.NavigationTest' + NONCE
TRACE = '''wl_seat@1.get_pointer(new id wl_pointer@2)
wl_pointer@2.enter(10, wl_surface@3, 0.0, 0.0)
wl_pointer@2.button(11, 100, 272, 1)
xdg_activation_v1@4.get_activation_token(new id xdg_activation_token_v1@5)
xdg_activation_token_v1@5.set_serial(11, wl_seat@1)
xdg_activation_token_v1@5.set_surface(wl_surface@3)
xdg_activation_token_v1@5.commit()
xdg_activation_token_v1@5.done("opaque-native-token")
wl_pointer@2.button(12, 101, 272, 0)
'''
TOKEN_EVENT = '''signal time=1 sender=:1.2 -> destination=(null destination) serial=11 path=/org/freedesktop/Notifications; interface=org.freedesktop.Notifications; member=ActivationToken
   uint32 7
   string "opaque-native-token"
'''
ACTION_EVENT = '''signal time=2 sender=:1.2 -> destination=(null destination) serial=12 path=/org/freedesktop/Notifications; interface=org.freedesktop.Notifications; member=ActionInvoked
   uint32 7
   string "default"
'''
ACTIVATE_EVENT = '''method call time=3 sender=:1.3 -> destination=''' + APP + ''' serial=42 path=/''' + APP.replace('.', '/') + '''; interface=org.freedesktop.Application; member=ActivateAction
   string "open"
   array [
      variant string "notification-navigation-test:''' + NONCE + '''"
   ]
   array [
      dict entry(
         string "activation-token"
         variant string "opaque-native-token"
      )
      dict entry(
         string "desktop-startup-id"
         variant string "opaque-native-token"
      )
   ]
'''
EVENTS = [TOKEN_EVENT, ACTION_EVENT, ACTIVATE_EVENT]
PRE_CLICK = TRACE.index('wl_pointer@2.button')


def chain(trace=TRACE, events=None, gtk=':1.3', native_id=7):
    return protocol.native_click_chain(trace, PRE_CLICK, EVENTS if events is None else events,
        ':1.2', gtk, APP, NONCE, native_id, '3', '2')


class NativeChain(unittest.TestCase):
    def test_owned_wire_chain_forwards_the_opaque_token(self):
        value = chain()
        self.assertEqual(value['token'], 'opaque-native-token')
        self.assertTrue(value['forwarded'])

    def test_another_sender_or_notification_cannot_supply_the_chain(self):
        for gtk, native_id in ((':1.9', 7), (':1.3', 9)):
            with self.subTest(gtk=gtk, native_id=native_id), self.assertRaises(RuntimeError):
                chain(gtk=gtk, native_id=native_id)

    def test_serial_and_surface_must_belong_to_the_native_button(self):
        for trace in (TRACE.replace('set_serial(11,', 'set_serial(99,'),
                      TRACE.replace('set_surface(wl_surface@3)', 'set_surface(wl_surface@9)'),
                      TRACE.replace('wl_pointer@2.button(11,', 'wl_pointer@9.button(11,')):
            with self.subTest(trace=trace), self.assertRaises(RuntimeError):
                chain(trace=trace)

    def test_lost_surface_or_repeated_action_is_rejected(self):
        with self.assertRaises(RuntimeError):
            chain(trace=TRACE.replace('xdg_activation_v1@4.', 'wl_pointer@2.leave(11, wl_surface@3)\nxdg_activation_v1@4.'))
        with self.assertRaises(RuntimeError):
            chain(events=EVENTS + [ACTIVATE_EVENT])

    def test_callback_cannot_precede_the_native_action(self):
        with self.assertRaises(RuntimeError):
            chain(events=[TOKEN_EVENT, ACTIVATE_EVENT, ACTION_EVENT])

    def test_old_backend_omission_is_evidence_not_token_forwarding(self):
        missing = ACTIVATE_EVENT[:ACTIVATE_EVENT.index('   array [\n      dict entry(')] + '   array [\n   ]\n'
        self.assertFalse(chain(events=[TOKEN_EVENT, ACTION_EVENT, missing])['forwarded'])


if __name__ == '__main__':
    unittest.main()
