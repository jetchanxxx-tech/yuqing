// 盘古舆情官网交互脚本

document.addEventListener('DOMContentLoaded', function() {
  // 平滑滚动
  document.querySelectorAll('a[href^="#"]').forEach(anchor => {
    anchor.addEventListener('click', function (e) {
      e.preventDefault();
      const target = document.querySelector(this.getAttribute('href'));
      if (target) {
        target.scrollIntoView({
          behavior: 'smooth',
          block: 'start'
        });
      }
    });
  });

  // 导航栏滚动效果
  let lastScroll = 0;
  const navbar = document.querySelector('.navbar');

  window.addEventListener('scroll', () => {
    const currentScroll = window.pageYOffset;

    if (currentScroll <= 0) {
      navbar.classList.remove('scroll-up');
      return;
    }

    if (currentScroll > lastScroll && !navbar.classList.contains('scroll-down')) {
      // 向下滚动
      navbar.classList.remove('scroll-up');
      navbar.classList.add('scroll-down');
    } else if (currentScroll < lastScroll && navbar.classList.contains('scroll-down')) {
      // 向上滚动
      navbar.classList.remove('scroll-down');
      navbar.classList.add('scroll-up');
    }
    lastScroll = currentScroll;
  });

  // FAQ 折叠
  document.querySelectorAll('.faq-question').forEach(question => {
    question.style.cursor = 'pointer';
    question.addEventListener('click', function() {
      const answer = this.nextElementSibling;
      const isVisible = answer.style.display === 'block';

      // 关闭所有其他答案
      document.querySelectorAll('.faq-answer').forEach(a => {
        a.style.display = 'none';
      });

      // 切换当前答案
      answer.style.display = isVisible ? 'none' : 'block';
    });
  });

  // 默认隐藏所有答案
  document.querySelectorAll('.faq-answer').forEach(answer => {
    answer.style.display = 'none';
  });

  // 数字动画（当元素进入视口时）
  const observerOptions = {
    threshold: 0.5,
    rootMargin: '0px 0px -100px 0px'
  };

  const observer = new IntersectionObserver((entries) => {
    entries.forEach(entry => {
      if (entry.isIntersecting) {
        entry.target.classList.add('animate');
        observer.unobserve(entry.target);
      }
    });
  }, observerOptions);

  // 观察需要动画的元素
  document.querySelectorAll('.stat-value, .pricing-price, .advantage-number').forEach(el => {
    observer.observe(el);
  });

  // 移动端菜单切换
  const mobileMenuBtn = document.createElement('button');
  mobileMenuBtn.className = 'mobile-menu-btn';
  mobileMenuBtn.innerHTML = '☰';
  mobileMenuBtn.style.display = 'none';

  if (window.innerWidth <= 768) {
    document.querySelector('.nav-left').appendChild(mobileMenuBtn);
    mobileMenuBtn.style.display = 'block';

    mobileMenuBtn.addEventListener('click', () => {
      const navCenter = document.querySelector('.nav-center');
      navCenter.style.display = navCenter.style.display === 'flex' ? 'none' : 'flex';
    });
  }

  // 窗口调整大小时重新检查
  window.addEventListener('resize', () => {
    if (window.innerWidth > 768) {
      document.querySelector('.nav-center').style.display = 'flex';
      mobileMenuBtn.style.display = 'none';
    } else {
      document.querySelector('.nav-center').style.display = 'none';
      mobileMenuBtn.style.display = 'block';
    }
  });

  // 卡片悬停效果增强
  document.querySelectorAll('.feature-card, .pricing-card').forEach(card => {
    card.addEventListener('mouseenter', function() {
      this.style.transition = 'transform 0.3s ease, box-shadow 0.3s ease';
    });
  });

  // 跟踪 CTA 点击（可接入分析工具）
  document.querySelectorAll('.btn-primary, .btn-hero').forEach(btn => {
    btn.addEventListener('click', function(e) {
      const btnText = this.textContent.trim();
      console.log('CTA Clicked:', btnText);
      // 这里可以接入 Google Analytics 或其他分析工具
      // gtag('event', 'cta_click', { button_text: btnText });
    });
  });
});

// 添加 CSS 动画类
const style = document.createElement('style');
style.textContent = `
  .navbar.scroll-down {
    transform: translateY(-100%);
  }

  .navbar.scroll-up {
    transform: translateY(0);
  }

  .navbar {
    transition: transform 0.3s ease;
  }

  .mobile-menu-btn {
    background: none;
    border: none;
    font-size: 24px;
    cursor: pointer;
    color: var(--text-primary);
    padding: 8px;
  }

  @media (max-width: 768px) {
    .nav-center {
      position: absolute;
      top: 100%;
      left: 0;
      right: 0;
      background: white;
      flex-direction: column;
      padding: 16px;
      box-shadow: 0 4px 12px var(--shadow);
      display: none;
    }

    .nav-center.active {
      display: flex;
    }
  }

  @keyframes fadeInUp {
    from {
      opacity: 0;
      transform: translateY(20px);
    }
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }

  .animate {
    animation: fadeInUp 0.6s ease;
  }
`;
document.head.appendChild(style);
