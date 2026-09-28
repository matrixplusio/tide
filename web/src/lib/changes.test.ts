import { describe, expect, it } from 'vitest'
import { isNoiseCommit } from './changes'

describe('isNoiseCommit', () => {
  it('hides merges and the pipeline\'s own naming commit', () => {
    for (const t of ["Merge remote-tracking branch 'origin/release/1.2'", "Merge branch 'feature/x' into dev", 'Merge pull request #12 from a/b', 'build:order-api', 'build order-api', 'BUILD: x']) {
      expect(isNoiseCommit(t), t).toBe(true)
    }
  })
  it('keeps everything a person wrote, whatever the house style', () => {
    for (const t of ['fix:修复了免登录页面接口携带token过期导致401的问题', 'feat:注单记录--新增房间号过滤', '添加screenId字段', '大厅回调改为订阅MQ', 'Merged the two configs into one', 'rebuild index nightly']) {
      expect(isNoiseCommit(t), t).toBe(false)
    }
  })
})
