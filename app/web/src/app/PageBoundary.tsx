import { Component, type ErrorInfo, type ReactNode } from 'react'
import { RefreshCw } from 'lucide-react'
import { useT } from '../i18n'
import { Banner, Button } from '../ui'
import { strings } from './strings'

type Props = { resetKey: string; children: ReactNode }
type State = { error: Error | null }

// PageBoundary keeps a failing page from taking the whole application down: the page is
// replaced by an error card, the menus stay, and going to another address tries again.
export class PageBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: unknown): State {
    return { error: error instanceof Error ? error : new Error(String(error)) }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('page failed', error, info.componentStack)
  }

  componentDidUpdate(prev: Props) {
    if (this.state.error && prev.resetKey !== this.props.resetKey) this.setState({ error: null })
  }

  render() {
    return this.state.error ? <PageCrash error={this.state.error} /> : this.props.children
  }
}

function PageCrash({ error }: { error: Error }) {
  const t = useT(strings)
  return (
    <div className="stack page-crash">
      <Banner kind="error" title={t('crash.title')}>
        <p>{t('crash.text')}</p>
        <code className="page-crash-message">{error.message || error.name}</code>
      </Banner>
      <div className="row">
        <Button variant="primary" onClick={() => window.location.reload()}>
          <RefreshCw size={15} aria-hidden />
          {t('crash.reload')}
        </Button>
      </div>
    </div>
  )
}
