import { ExternalLink } from 'lucide-react'
import type { ReactNode } from 'react'

import { appMessages } from '@/app/messages'
import { Button } from '@/components/ui/button'
import { CopyButton } from '@/components/ui/copy-button'
import { Card, CardHeader, PageHeader } from '@/components/ui/page'
import { ErrorState, Loading } from '@/components/ui/states'
import { useStatus } from '@/features/service/queries'
import { formatFullDateTime } from '@/lib/format'
import { defineMessages, useI18n, useMessages } from '@/lib/i18n'

// aboutMessages 是关于页的文案
const aboutMessages = defineMessages({
  'zh-CN': {
    title: '关于',
    tagline: 'Google Gemini Web 的多账号 API 反代',
    version: '版本',
    host: '运行方式',
    hostWeb: '浏览器访问',
    hostDesktop: '桌面窗口',
    listen: '监听地址',
    endpoint: '客户端接口地址',
    dataDir: '数据目录',
    started: '启动时间',
    homepage: '项目主页',
    releases: '发布页',
    copied: '已复制',
    copyEndpoint: '复制接口地址',
    loadFailed: '无法读取服务状态',
  },
  'zh-TW': {
    title: '關於',
    tagline: 'Google Gemini Web 的多帳號 API 反向代理',
    version: '版本',
    host: '執行方式',
    hostWeb: '瀏覽器存取',
    hostDesktop: '桌面視窗',
    listen: '監聽位址',
    endpoint: 'API 網址',
    dataDir: '資料目錄',
    started: '啟動時間',
    homepage: '專案首頁',
    releases: '發布頁面',
    copied: '已複製',
    copyEndpoint: '複製 API 網址',
    loadFailed: '無法讀取服務狀態',
  },
  en: {
    title: 'About',
    tagline: 'Multi-account API proxy for Google Gemini Web',
    version: 'Version',
    host: 'Running as',
    hostWeb: 'Browser',
    hostDesktop: 'Desktop window',
    listen: 'Listen address',
    endpoint: 'Client endpoint',
    dataDir: 'Data directory',
    started: 'Started',
    homepage: 'Homepage',
    releases: 'Releases',
    copied: 'Copied',
    copyEndpoint: 'Copy endpoint',
    loadFailed: 'Could not read service status',
  },
  ja: {
    title: 'このアプリについて',
    tagline: 'Google Gemini Web 向けのマルチアカウント API プロキシ',
    version: 'バージョン',
    host: '実行形態',
    hostWeb: 'ブラウザー',
    hostDesktop: 'デスクトップウィンドウ',
    listen: '待ち受けアドレス',
    endpoint: 'クライアント用エンドポイント',
    dataDir: 'データディレクトリ',
    started: '起動日時',
    homepage: 'プロジェクトページ',
    releases: 'リリース',
    copied: 'コピーしました',
    copyEndpoint: 'エンドポイントをコピー',
    loadFailed: 'サービスの状態を読み込めません',
  },
  ko: {
    title: '정보',
    tagline: 'Google Gemini Web용 다중 계정 API 프록시',
    version: '버전',
    host: '실행 방식',
    hostWeb: '브라우저',
    hostDesktop: '데스크톱 창',
    listen: '수신 주소',
    endpoint: '클라이언트 엔드포인트',
    dataDir: '데이터 디렉터리',
    started: '시작 시각',
    homepage: '프로젝트 홈페이지',
    releases: '릴리스',
    copied: '복사됨',
    copyEndpoint: '엔드포인트 복사',
    loadFailed: '서비스 상태를 불러올 수 없습니다',
  },
  fr: {
    title: 'À propos',
    tagline: 'Proxy d’API multicompte pour Google Gemini Web',
    version: 'Version',
    host: 'Exécution',
    hostWeb: 'Navigateur',
    hostDesktop: 'Fenêtre de bureau',
    listen: 'Adresse d’écoute',
    endpoint: 'Point de terminaison client',
    dataDir: 'Répertoire de données',
    started: 'Démarré le',
    homepage: 'Page du projet',
    releases: 'Versions publiées',
    copied: 'Copié',
    copyEndpoint: 'Copier le point de terminaison',
    loadFailed: 'Impossible de lire l’état du service',
  },
  de: {
    title: 'Über',
    tagline: 'Multi-Konto-API-Proxy für Google Gemini Web',
    version: 'Version',
    host: 'Ausführung',
    hostWeb: 'Browser',
    hostDesktop: 'Desktop-Fenster',
    listen: 'Listen-Adresse',
    endpoint: 'Client-Endpunkt',
    dataDir: 'Datenverzeichnis',
    started: 'Gestartet',
    homepage: 'Projektseite',
    releases: 'Releases',
    copied: 'Kopiert',
    copyEndpoint: 'Endpunkt kopieren',
    loadFailed: 'Dienststatus konnte nicht gelesen werden',
  },
})

// Row 是关于页的一行键值
function Row({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="flex flex-col gap-1 border-t border-border py-3 first:border-t-0 sm:flex-row sm:items-center sm:justify-between">
      <dt className="text-sm text-foreground-muted">{label}</dt>
      <dd className="font-mono break-all">{value}</dd>
    </div>
  )
}

// AboutPage 展示版本、运行方式与项目链接
export function AboutPage() {
  const m = useMessages(aboutMessages)
  const app = useMessages(appMessages)
  const { locale } = useI18n()
  const status = useStatus()
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
  return (
    <>
      <PageHeader title={m.title} description={m.tagline} />
      <Card>
        <CardHeader title="Gemini-Web2API" />
        <dl className="px-5 py-1">
          <Row label={m.version} value={data.version} />
          <Row label={m.host} value={data.desktop ? m.hostDesktop : m.hostWeb} />
          <Row label={m.listen} value={data.listen_addr} />
          <Row
            label={m.endpoint}
            value={
              <span className="inline-flex items-center gap-2">
                {data.base_url}
                <CopyButton value={data.base_url} label={m.copyEndpoint} copiedLabel={m.copied} />
              </span>
            }
          />
          <Row label={m.dataDir} value={data.data_dir} />
          <Row label={m.started} value={formatFullDateTime(new Date(data.started_at), locale)} />
        </dl>
      </Card>
      <div className="flex flex-wrap gap-3">
        <Button variant="secondary" asChild>
          <a href="https://github.com/Mag1cFall/Gemini-Web2API" target="_blank" rel="noreferrer">
            {m.homepage}
            <ExternalLink size={16} aria-hidden />
          </a>
        </Button>
        <Button variant="secondary" asChild>
          <a
            href="https://github.com/Mag1cFall/Gemini-Web2API/releases"
            target="_blank"
            rel="noreferrer"
          >
            {m.releases}
            <ExternalLink size={16} aria-hidden />
          </a>
        </Button>
      </div>
    </>
  )
}
