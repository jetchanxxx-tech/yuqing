# 盘古舆情官网

## 简介

盘古舆情产品官网，基于项目 PRD、竞品分析和产品功能设计。

## 文件说明

- `index.html` - 官网主页（单页应用）
- `landing.css` - 样式文件
- `landing.js` - 交互脚本
- `assets/` - 图片和资源文件夹（待添加产品截图）

## 内容结构

1. **Hero 区域** - 核心价值主张 + CTA
2. **对比表格** - 盘古舆情 vs 传统工具
3. **核心功能** - 6 大功能卡片
4. **产品优势** - 5 大差异化优势
5. **定价方案** - 4 个套餐（体验/速览/研判/旗舰）
6. **产品演示** - "雅阁后排"案例展示
7. **技术架构** - 技术栈和开源信息
8. **FAQ** - 常见问题解答
9. **CTA** - 注册引导
10. **页脚** - 导航和公司信息

## 本地预览

直接在浏览器中打开 `index.html` 即可预览。

或者使用简单的 HTTP 服务器：

```bash
# Python 3
python -m http.server 8000

# Node.js (http-server)
npx http-server -p 8000

# 然后访问 http://localhost:8000
```

## 部署

### 方式 1：Nginx 静态托管

```nginx
server {
    listen 80;
    server_name yuqing.pangu-cloud.com;

    root /var/www/landing;
    index index.html;

    location / {
        try_files $uri $uri/ /index.html;
    }

    # Gzip 压缩
    gzip on;
    gzip_types text/css application/javascript text/html;
}
```

### 方式 2：GitHub Pages

1. 将 `landing/` 目录内容推送到 `gh-pages` 分支
2. 在仓库设置中启用 GitHub Pages
3. 访问 `https://jetchanxxx-tech.github.io/yuqing/`

### 方式 3：Vercel / Netlify

直接将 `landing/` 目录部署到 Vercel 或 Netlify。

## 待优化

### 高优先级
- [ ] 添加真实产品截图（替换占位符）
- [ ] 添加产品演示视频
- [ ] 接入真实的注册/登录链接
- [ ] 添加 Google Analytics / 百度统计

### 中优先级
- [ ] 添加客户案例/Logo
- [ ] 添加团队介绍
- [ ] 添加博客/资源中心
- [ ] SEO 优化（meta 标签、sitemap）

### 低优先级
- [ ] 多语言支持（英文版）
- [ ] 暗色模式
- [ ] 在线客服集成
- [ ] 邮件订阅功能

## 性能优化

- 使用 CDN 加速静态资源
- 图片懒加载
- CSS/JS 压缩
- 启用 HTTP/2
- 配置 Gzip 压缩

## 联系方式

- 邮箱：support@pangu-cloud.com
- GitHub：https://github.com/jetchanxxx-tech/yuqing
- 产品地址：https://yuqing2.pangu-cloud.com
