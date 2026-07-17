# 界面截图

下面每一张都来自**真实运行中的 agent 团队**,不是概念图。

## 多 Agent 并行协作

十几个 agent 同时干活:分屏对照、互派任务、彼此审查。左侧团队面板实时显示每个 agent 的模型与本轮花费。

![多 Agent 分屏协作](/screenshots/tour-split.png)

## 完整工作区

文件树、编辑器、对话同屏 —— agent 改的每个文件即点即看。

![工作区与文件浏览器](/screenshots/tour-files.png)

## 成本透视

每轮请求的 token、成本、缓存命中率实时可见。图中这轮:5.58m token、$6.51、**99.6% 缓存命中** —— 高命中是工程,不是运气。面板还带缓存诊断,命中掉了会告诉你是谁把前缀弄脏的。

![用量与成本分析面板](/screenshots/tour-analysis.png)

## 团队知识图谱

agent 沉淀的经验入库评审、成典共享:正典 / 待评审 / 草案分级,按域组织。团队越用越聪明。

![团队知识图谱](/screenshots/tour-knowledge.png)

## AI 流量审计

每个发给模型的请求都过安检(MITM 抓包):令牌、密钥外泄实时告警,可拦截、可加白、可看脱敏快照。

![AI 流量审计日志](/screenshots/tour-audit.png)

## 口袋里的团队

agent 列表、对话、实时终端 —— 手机经 Hub 直连,在哪都能接管。

![cicy-mobile 手机端](/screenshots/tour-mobile.png)

## 多端一个入口

桌面端把本地、Docker、远程 Hub 的团队聚合成一页。

![cicy-desktop 团队入口](/screenshots/tour-desktop.png)

---

想自己跑起来?[快速开始](/guide/getting-started) 五分钟,或先看 [下载与安装](/guide/download)。
