import type { DeliveryLevel } from '../api/analyses';

export const DELIVERY_LABELS: Record<DeliveryLevel, string> = {
  full: '完整研判', limited: '受限研判', brief: '资料简报', empty: '空结果回执',
};
export const DELIVERY_COLORS: Record<DeliveryLevel, string> = {
  full: 'success', limited: 'warning', brief: 'default', empty: 'default',
};
