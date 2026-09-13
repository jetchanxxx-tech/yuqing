import { Button, Empty, Skeleton, Result } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';

/** 卡片内的加载骨架 */
export function LoadingBlock({ rows = 3 }: { rows?: number }) {
  return <Skeleton active paragraph={{ rows }} title={false} />;
}

/** 卡片内的错误块 + 重试 */
export function ErrorBlock({ description, onRetry }: { description?: string; onRetry?: () => void }) {
  return (
    <div style={{ textAlign: 'center', padding: '16px 0' }}>
      <Empty
        image={Empty.PRESENTED_IMAGE_SIMPLE}
        description={description ?? '数据加载失败'}
      >
        {onRetry && (
          <Button size="small" icon={<ReloadOutlined />} onClick={onRetry}>
            重试
          </Button>
        )}
      </Empty>
    </div>
  );
}

/** 页面级空态 */
export function EmptyBlock({ description }: { description?: string }) {
  return <Empty description={description ?? '暂无数据'} style={{ padding: '32px 0' }} />;
}

/** 页面级加载 */
export function PageLoading({ rows = 6 }: { rows?: number }) {
  return (
    <div style={{ padding: 24, background: '#fff', borderRadius: 16 }}>
      <Skeleton active paragraph={{ rows }} />
    </div>
  );
}

/** 页面级错误（占满整页卡片） */
export function PageError({ description, onRetry }: { description?: string; onRetry?: () => void }) {
  return (
    <div style={{ padding: 24, background: '#fff', borderRadius: 16 }}>
      <Result
        status="error"
        title="加载失败"
        subTitle={description ?? '暂时无法获取数据，请稍后重试'}
        extra={
          onRetry && (
            <Button type="primary" icon={<ReloadOutlined />} onClick={onRetry}>
              重新加载
            </Button>
          )
        }
      />
    </div>
  );
}
