import { ExternalLink, RefreshCw } from 'lucide-react'
import { useEffect, useState } from 'react'

import { appMessages } from '@/app/messages'
import { Button } from '@/components/ui/button'
import { Callout } from '@/components/ui/callout'
import { Dialog, DialogClose, DialogContent, DialogTrigger } from '@/components/ui/dialog'
import { Card, CardHeader, CardRow } from '@/components/ui/page'
import { ErrorState, Loading } from '@/components/ui/states'
import { toast } from '@/components/ui/toast'
import {
  useApplyUpdate,
  useCheckUpdate,
  useRestartedVersion,
  useUpdateStatus,
} from '@/features/about/queries'
import { renderMarkdown } from '@/features/playground/markdown'
import { formatFullDateTime } from '@/lib/format'
import { defineMessages, fill, useI18n, useMessages } from '@/lib/i18n'

// updateMessages 是在线更新卡片的文案
const updateMessages = defineMessages({
  'zh-CN': {
    title: '在线更新',
    check: '检查更新',
    latest: '最新版本',
    published: '发布时间',
    checkedAt: '检查时间',
    upToDate: '已是最新版本',
    available: '发现新版本 {version}',
    notes: '更新内容',
    updateTo: '更新到 {version}',
    confirmHint: '下载 {version} 的发行包并校验 SHA-256，替换可执行文件后重启服务',
    confirm: '确认更新',
    cancel: '取消',
    restarting: '正在重启到 {version}，服务就绪后页面自动刷新',
    development: '源码构建不支持在线更新，新版本从发布页下载',
    container: 'Docker 部署随镜像更新，拉取新镜像后重建容器',
    checkFailed: '无法读取最新版本：{error}',
    viewRelease: '查看发布页',
    loadFailed: '无法读取更新状态',
  },
  'zh-TW': {
    title: '線上更新',
    check: '檢查更新',
    latest: '最新版本',
    published: '發布時間',
    checkedAt: '檢查時間',
    upToDate: '已是最新版本',
    available: '有新版本 {version}',
    notes: '更新內容',
    updateTo: '更新到 {version}',
    confirmHint: '下載 {version} 的發行套件並校驗 SHA-256，替換執行檔後重新啟動服務',
    confirm: '確認更新',
    cancel: '取消',
    restarting: '正在重新啟動到 {version}，服務就緒後頁面自動重新整理',
    development: '原始碼建置不支援線上更新，新版本請從發布頁面下載',
    container: 'Docker 部署隨映像更新，拉取新映像後重建容器',
    checkFailed: '無法讀取最新版本：{error}',
    viewRelease: '查看發布頁面',
    loadFailed: '無法讀取更新狀態',
  },
  en: {
    title: 'Online update',
    check: 'Check for updates',
    latest: 'Latest version',
    published: 'Published',
    checkedAt: 'Checked',
    upToDate: 'You are on the latest version',
    available: 'Version {version} is available',
    notes: 'What’s new',
    updateTo: 'Update to {version}',
    confirmHint:
      'Downloads the {version} package, verifies its SHA-256, replaces the executable and restarts the service',
    confirm: 'Confirm update',
    cancel: 'Cancel',
    restarting: 'Restarting into {version}; the page reloads when the service is ready',
    development: 'Source builds do not update online; download new versions from Releases',
    container:
      'Docker deployments update with the image; pull the new image and recreate the container',
    checkFailed: 'Could not read the latest version: {error}',
    viewRelease: 'View release',
    loadFailed: 'Could not read the update status',
  },
  ja: {
    title: 'オンライン更新',
    check: '更新を確認',
    latest: '最新バージョン',
    published: '公開日時',
    checkedAt: '確認日時',
    upToDate: '最新バージョンです',
    available: '新しいバージョン {version} があります',
    notes: '変更内容',
    updateTo: '{version} に更新',
    confirmHint:
      '{version} のパッケージをダウンロードして SHA-256 を検証し、実行ファイルを置き換えてサービスを再起動します',
    confirm: '更新する',
    cancel: 'キャンセル',
    restarting: '{version} で再起動しています。サービスの準備ができるとページを再読み込みします',
    development:
      'ソースからのビルドはオンライン更新に対応していません。新しいバージョンはリリースから入手してください',
    container:
      'Docker ではイメージごと更新します。新しいイメージを取得してコンテナーを作り直してください',
    checkFailed: '最新バージョンを読み込めません：{error}',
    viewRelease: 'リリースを表示',
    loadFailed: '更新状態を読み込めません',
  },
  ko: {
    title: '온라인 업데이트',
    check: '업데이트 확인',
    latest: '최신 버전',
    published: '게시 시각',
    checkedAt: '확인 시각',
    upToDate: '최신 버전입니다',
    available: '새 버전 {version}을(를) 사용할 수 있습니다',
    notes: '변경 내용',
    updateTo: '{version}(으)로 업데이트',
    confirmHint:
      '{version} 패키지를 내려받아 SHA-256을 검증하고 실행 파일을 교체한 뒤 서비스를 다시 시작합니다',
    confirm: '업데이트',
    cancel: '취소',
    restarting: '{version}(으)로 다시 시작하는 중입니다. 서비스가 준비되면 페이지를 새로 고칩니다',
    development:
      '소스 빌드는 온라인 업데이트를 지원하지 않습니다. 새 버전은 릴리스에서 내려받으세요',
    container:
      'Docker 배포는 이미지로 업데이트합니다. 새 이미지를 받은 뒤 컨테이너를 다시 만드세요',
    checkFailed: '최신 버전을 불러올 수 없습니다: {error}',
    viewRelease: '릴리스 보기',
    loadFailed: '업데이트 상태를 불러올 수 없습니다',
  },
  fr: {
    title: 'Mise à jour en ligne',
    check: 'Rechercher une mise à jour',
    latest: 'Dernière version',
    published: 'Publiée le',
    checkedAt: 'Vérifiée le',
    upToDate: 'Vous utilisez la dernière version',
    available: 'La version {version} est disponible',
    notes: 'Nouveautés',
    updateTo: 'Mettre à jour vers {version}',
    confirmHint:
      'Télécharge le paquet {version}, vérifie son SHA-256, remplace l’exécutable et redémarre le service',
    confirm: 'Confirmer la mise à jour',
    cancel: 'Annuler',
    restarting: 'Redémarrage en {version} ; la page se recharge quand le service est prêt',
    development:
      'Les builds depuis les sources ne se mettent pas à jour en ligne ; téléchargez les nouvelles versions depuis Releases',
    container:
      'Les déploiements Docker se mettent à jour avec l’image ; récupérez la nouvelle image et recréez le conteneur',
    checkFailed: 'Impossible de lire la dernière version : {error}',
    viewRelease: 'Voir la version',
    loadFailed: 'Impossible de lire l’état de mise à jour',
  },
  de: {
    title: 'Online-Update',
    check: 'Nach Updates suchen',
    latest: 'Neueste Version',
    published: 'Veröffentlicht',
    checkedAt: 'Geprüft',
    upToDate: 'Sie verwenden die neueste Version',
    available: 'Version {version} ist verfügbar',
    notes: 'Neuerungen',
    updateTo: 'Auf {version} aktualisieren',
    confirmHint:
      'Lädt das Paket {version} herunter, prüft die SHA-256, ersetzt die ausführbare Datei und startet den Dienst neu',
    confirm: 'Update bestätigen',
    cancel: 'Abbrechen',
    restarting: 'Neustart mit {version}; die Seite lädt neu, sobald der Dienst bereit ist',
    development:
      'Builds aus dem Quellcode werden nicht online aktualisiert; neue Versionen gibt es unter Releases',
    container:
      'Docker-Installationen werden mit dem Image aktualisiert; neues Image laden und Container neu erstellen',
    checkFailed: 'Neueste Version konnte nicht gelesen werden: {error}',
    viewRelease: 'Release ansehen',
    loadFailed: 'Update-Status konnte nicht gelesen werden',
  },
})

// releaseNotesClass 是发行说明的 Markdown 排版
const releaseNotesClass =
  'text-sm break-words [&_a]:text-primary [&_a]:underline [&_code]:rounded-sm [&_code]:bg-muted [&_code]:px-1 [&_code]:font-mono [&_h2]:font-semibold [&_h3]:font-semibold [&_li+li]:mt-1 [&_ol]:list-decimal [&_ol]:pl-6 [&_ul]:list-disc [&_ul]:pl-6 [&>*+*]:mt-2'

// UpdateCard 检查 GitHub 最新发行版本，确认后下载校验并重启到新版本，重启后按版本变化刷新页面
export function UpdateCard() {
  const m = useMessages(updateMessages)
  const app = useMessages(appMessages)
  const { locale } = useI18n()
  const status = useUpdateStatus()
  const check = useCheckUpdate()
  const apply = useApplyUpdate()
  const [confirming, setConfirming] = useState(false)
  const [restartFrom, setRestartFrom] = useState('')
  const restarted = useRestartedVersion(restartFrom !== '')
  useEffect(() => {
    if (restartFrom && restarted.data && restarted.data !== restartFrom) window.location.reload()
  }, [restartFrom, restarted.data])

  if (status.isPending) return <Loading label={app.loading} />
  if (status.isError) {
    return (
      <ErrorState
        title={m.loadFailed}
        error={status.error}
        retryLabel={app.retry}
        onRetry={() => void status.refetch()}
      />
    )
  }
  const data = status.data
  const latest = data.latest ?? ''
  const restarting = restartFrom !== '' || data.updating
  const card = (
    <Card>
      <CardHeader
        title={m.title}
        actions={
          data.mode === 'binary' && !restarting ? (
            <Button
              variant="secondary"
              loading={check.isPending}
              onClick={() => {
                check.mutate(undefined, {
                  onError: (error) => {
                    toast({ tone: 'error', title: error.message })
                  },
                })
              }}
            >
              <RefreshCw size={16} aria-hidden />
              {m.check}
            </Button>
          ) : undefined
        }
      />
      {data.mode !== 'binary' && (
        <div className="p-5">
          <Callout tone="muted">{data.mode === 'container' ? m.container : m.development}</Callout>
        </div>
      )}
      {data.mode === 'binary' && data.error && (
        <div className="px-5 py-4">
          <Callout tone="error" role="alert">
            {fill(m.checkFailed, { error: data.error })}
          </Callout>
        </div>
      )}
      {data.mode === 'binary' && latest && (
        <>
          <CardRow
            title={data.available ? fill(m.available, { version: latest }) : m.upToDate}
            description={[
              data.published_at &&
                m.published + ' ' + formatFullDateTime(new Date(data.published_at), locale),
              data.checked_at &&
                m.checkedAt + ' ' + formatFullDateTime(new Date(data.checked_at), locale),
            ]
              .filter(Boolean)
              .join(' · ')}
          >
            {data.url && (
              <Button variant="ghost" asChild>
                <a href={data.url} target="_blank" rel="noreferrer">
                  {m.viewRelease}
                  <ExternalLink size={16} aria-hidden />
                </a>
              </Button>
            )}
            {data.available && (
              <Dialog open={confirming} onOpenChange={setConfirming}>
                <DialogTrigger asChild>
                  <Button variant="primary" disabled={restarting}>
                    {fill(m.updateTo, { version: latest })}
                  </Button>
                </DialogTrigger>
                <DialogContent
                  title={fill(m.updateTo, { version: latest })}
                  description={fill(m.confirmHint, { version: latest })}
                  closeLabel={m.cancel}
                  footer={
                    <>
                      <DialogClose asChild>
                        <Button variant="secondary">{m.cancel}</Button>
                      </DialogClose>
                      <Button
                        variant="primary"
                        loading={apply.isPending}
                        onClick={() => {
                          apply.mutate(undefined, {
                            onSuccess: (next) => {
                              setConfirming(false)
                              setRestartFrom(next.current)
                            },
                            onError: (error) => {
                              toast({ tone: 'error', title: error.message })
                            },
                          })
                        }}
                      >
                        {m.confirm}
                      </Button>
                    </>
                  }
                >
                  <span className="sr-only">{m.confirmHint}</span>
                </DialogContent>
              </Dialog>
            )}
          </CardRow>
          {restarting && (
            <div className="px-5 py-4">
              <Callout tone="muted" role="status">
                {fill(m.restarting, { version: latest })}
              </Callout>
            </div>
          )}
        </>
      )}
    </Card>
  )
  if (!(data.mode === 'binary' && data.available && data.notes)) return card
  return (
    <>
      {card}
      <Card className="flex flex-col lg:min-h-[160px] lg:grow lg:basis-0">
        <CardHeader title={m.notes} />
        <div
          className={`px-5 py-4 lg:min-h-0 lg:grow lg:overflow-y-auto ${releaseNotesClass}`}
          dangerouslySetInnerHTML={{ __html: renderMarkdown(data.notes) }}
        />
      </Card>
    </>
  )
}
