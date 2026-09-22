// Readable names for audit actions; an unknown action shows its key.
//
// These are deliberately English and deliberately NOT in the i18n catalogs:
// an audit trail is evidence, so a record must read the same for everyone
// looking at it, whatever language the rest of the console is in.
// internal/store/pg/audit_labels_test.go fails when the server writes an
// action that has no name here.
export const actionLabel: Record<string, string> = {
  // sign-in and sessions
  'auth.login': 'Signed in',
  'auth.login.failed': 'Sign-in failed',
  'auth.login.denied': 'Sign-in denied',
  'auth.login.locked': 'Sign-in locked out',
  'auth.captcha.failed': 'Captcha failed',
  'auth.session.revoke': 'Session revoked',
  'permission.denied': 'Permission denied',

  // releases
  'release.create': 'Release created',
  'release.submit': 'Submitted for confirmation',
  'release.confirm': 'Confirmed',
  'release.confirm.auto': 'Released without a person',
  'release.confirm.denied': 'Confirmation denied',
  'release.approve': 'Approved',
  'release.reject': 'Rejected',
  'release.cancel': 'Cancelled',
  'release.succeeded': 'Release succeeded',
  'release.failed': 'Release failed',
  'release.cancelled': 'Release cancelled',
  'release.rejected': 'Release rejected',

  // release items
  'item.execute': 'Promotion created',
  'item.succeeded': 'Item succeeded',
  'item.failed': 'Item failed',
  'item.skipped': 'Item skipped',
  'item.cancelled': 'Item cancelled',

  // accounts, groups, roles
  'user.create': 'User created',
  'user.rename': 'User renamed',
  'user.password': 'Password reset',
  'user.enable': 'User enabled',
  'user.disable': 'User disabled',
  'user.sessions.revoke': 'User sessions revoked',
  'group.create': 'Group created',
  'group.update': 'Group updated',
  'group.delete': 'Group deleted',
  'group.members.add': 'Members added',
  'group.members.remove': 'Members removed',
  'role.create': 'Role created',
  'role.update': 'Role updated',
  'role.delete': 'Role deleted',
  'role.bind': 'Role granted',
  'role.bind.update': 'Grant scope changed',
  'role.unbind': 'Role revoked',

  // CI-triggered releases
  'ci.intake': 'CI reported an image',
  'ci.token.create': 'CI token issued',
  'ci.token.revoke': 'CI token revoked',

  // settings and setup
  'settings.update': 'Settings changed',
  'settings.notify.test': 'Test notification sent',
  'setup.admin': 'Became administrator',
  'setup.finish': 'Setup finished',
  'setup.token.failed': 'Setup token rejected',
}
