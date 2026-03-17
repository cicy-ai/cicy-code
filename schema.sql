-- MySQL dump 10.13  Distrib 8.0.45, for Linux (x86_64)
--
-- Host: localhost    Database: cicy_code
-- ------------------------------------------------------
-- Server version	8.0.45

/*!40101 SET @OLD_CHARACTER_SET_CLIENT=@@CHARACTER_SET_CLIENT */;
/*!40101 SET @OLD_CHARACTER_SET_RESULTS=@@CHARACTER_SET_RESULTS */;
/*!40101 SET @OLD_COLLATION_CONNECTION=@@COLLATION_CONNECTION */;
/*!50503 SET NAMES utf8mb4 */;
/*!40103 SET @OLD_TIME_ZONE=@@TIME_ZONE */;
/*!40103 SET TIME_ZONE='+00:00' */;
/*!40014 SET @OLD_UNIQUE_CHECKS=@@UNIQUE_CHECKS, UNIQUE_CHECKS=0 */;
/*!40014 SET @OLD_FOREIGN_KEY_CHECKS=@@FOREIGN_KEY_CHECKS, FOREIGN_KEY_CHECKS=0 */;
/*!40101 SET @OLD_SQL_MODE=@@SQL_MODE, SQL_MODE='NO_AUTO_VALUE_ON_ZERO' */;
/*!40111 SET @OLD_SQL_NOTES=@@SQL_NOTES, SQL_NOTES=0 */;

--
-- Table structure for table `agent_config`
--

DROP TABLE IF EXISTS `agent_config`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `agent_config` (
  `id` int NOT NULL AUTO_INCREMENT,
  `pane_id` varchar(255) NOT NULL,
  `node_url` varchar(255) DEFAULT 'http://localhost:13431',
  `title` varchar(255) DEFAULT NULL,
  `ttyd_port` int NOT NULL,
  `workspace` varchar(500) DEFAULT NULL,
  `init_script` varchar(500) DEFAULT NULL,
  `proxy` varchar(500) DEFAULT NULL,
  `tg_token` varchar(200) DEFAULT NULL,
  `tg_chat_id` varchar(100) DEFAULT NULL,
  `tg_enable` tinyint(1) DEFAULT '0',
  `created_at` timestamp NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` timestamp NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `ttyd_pid` int DEFAULT NULL,
  `active` tinyint(1) NOT NULL DEFAULT '1',
  `private_mode` tinyint(1) DEFAULT '0',
  `allowed_users` text,
  `proxy_enable` tinyint(1) DEFAULT '0' COMMENT '是否启用HTTP代理(使用proxy字段)',
  `agent_duty` text,
  `preview` text,
  `config` text,
  `ttyd_preview` text,
  `agent_type` varchar(100) DEFAULT '',
  `common_prompt` longtext,
  `role` varchar(20) DEFAULT NULL,
  `default_model` varchar(50) DEFAULT NULL,
  `trust_level` varchar(20) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `pane_id` (`pane_id`),
  KEY `idx_pane_id` (`pane_id`),
  KEY `idx_port` (`ttyd_port`)
) ENGINE=InnoDB AUTO_INCREMENT=221 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `agent_groups`
--

DROP TABLE IF EXISTS `agent_groups`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `agent_groups` (
  `id` int NOT NULL AUTO_INCREMENT,
  `name` varchar(100) NOT NULL,
  `description` varchar(255) DEFAULT '',
  `created_at` datetime DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB AUTO_INCREMENT=24 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `agent_queue`
--

DROP TABLE IF EXISTS `agent_queue`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `agent_queue` (
  `id` int NOT NULL AUTO_INCREMENT,
  `pane_id` varchar(50) NOT NULL,
  `message` text NOT NULL,
  `type` varchar(20) DEFAULT 'message',
  `status` enum('pending','sent','done') DEFAULT 'pending',
  `priority` int DEFAULT '0',
  `created_at` datetime DEFAULT CURRENT_TIMESTAMP,
  `sent_at` datetime DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB AUTO_INCREMENT=44 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `auth_codes`
--

DROP TABLE IF EXISTS `auth_codes`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `auth_codes` (
  `code` varchar(64) NOT NULL,
  `user_id` varchar(64) NOT NULL,
  `slug` varchar(32) NOT NULL,
  `vm_token` varchar(255) NOT NULL,
  `created_at` datetime DEFAULT CURRENT_TIMESTAMP,
  `used` tinyint DEFAULT '0',
  PRIMARY KEY (`code`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `global_vars`
--

DROP TABLE IF EXISTS `global_vars`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `global_vars` (
  `key_name` varchar(255) NOT NULL,
  `value` text,
  PRIMARY KEY (`key_name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `group_windows`
--

DROP TABLE IF EXISTS `group_windows`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `group_windows` (
  `id` int NOT NULL AUTO_INCREMENT,
  `group_id` int NOT NULL,
  `win_id` varchar(100) NOT NULL,
  `win_type` enum('agent_ttyd','app_frame') NOT NULL DEFAULT 'agent_ttyd',
  `ref_id` varchar(100) DEFAULT NULL COMMENT 'pane_id for ttyd, app_id for app_frame',
  `pos_x` float NOT NULL DEFAULT '20',
  `pos_y` float NOT NULL DEFAULT '20',
  `width` float NOT NULL DEFAULT '480',
  `height` float NOT NULL DEFAULT '320',
  `z_index` int NOT NULL DEFAULT '1',
  `created_at` datetime DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_group_win` (`group_id`,`win_id`),
  CONSTRAINT `fk_gw_group` FOREIGN KEY (`group_id`) REFERENCES `agent_groups` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB AUTO_INCREMENT=146 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `http_log`
--

DROP TABLE IF EXISTS `http_log`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `http_log` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `pane_id` varchar(64) NOT NULL,
  `method` varchar(16) NOT NULL DEFAULT '',
  `url` text NOT NULL,
  `status_code` int DEFAULT '0',
  `req_kb` float DEFAULT '0',
  `res_kb` float DEFAULT '0',
  `data` json DEFAULT NULL,
  `ts` int NOT NULL,
  `created_at` timestamp NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_pane` (`pane_id`),
  KEY `idx_ts` (`ts`)
) ENGINE=InnoDB AUTO_INCREMENT=38947 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `pane_agents`
--

DROP TABLE IF EXISTS `pane_agents`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `pane_agents` (
  `id` int NOT NULL AUTO_INCREMENT,
  `pane_id` varchar(255) NOT NULL,
  `agent_name` varchar(255) NOT NULL,
  `status` varchar(50) DEFAULT 'active',
  `created_at` timestamp NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` timestamp NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `unique_pane_agent` (`pane_id`,`agent_name`),
  KEY `idx_pane_id` (`pane_id`),
  KEY `idx_agent_name` (`agent_name`)
) ENGINE=InnoDB AUTO_INCREMENT=112 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `saas_users`
--

DROP TABLE IF EXISTS `saas_users`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `saas_users` (
  `id` varchar(36) NOT NULL,
  `email` varchar(255) NOT NULL,
  `plan` varchar(20) DEFAULT 'free',
  `backend_url` varchar(255) DEFAULT '',
  `vm_url` varchar(255) DEFAULT '',
  `created_at` timestamp NULL DEFAULT CURRENT_TIMESTAMP,
  `vm_token` varchar(255) DEFAULT '',
  PRIMARY KEY (`id`),
  UNIQUE KEY `email` (`email`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `tokens`
--

DROP TABLE IF EXISTS `tokens`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `tokens` (
  `id` int NOT NULL AUTO_INCREMENT,
  `token` varchar(128) NOT NULL,
  `group_id` int DEFAULT NULL COMMENT '绑定桌面组, null=所有',
  `pane_id` varchar(64) DEFAULT NULL COMMENT '绑定pane, null=组内所有',
  `perms` varchar(255) NOT NULL COMMENT '逗号分隔权限',
  `note` varchar(255) DEFAULT NULL COMMENT '备注',
  `expires_at` datetime DEFAULT NULL COMMENT 'null=永不过期',
  `created_at` datetime DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `token` (`token`),
  KEY `idx_token` (`token`)
) ENGINE=InnoDB AUTO_INCREMENT=42 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40103 SET TIME_ZONE=@OLD_TIME_ZONE */;

/*!40101 SET SQL_MODE=@OLD_SQL_MODE */;
/*!40014 SET FOREIGN_KEY_CHECKS=@OLD_FOREIGN_KEY_CHECKS */;
/*!40014 SET UNIQUE_CHECKS=@OLD_UNIQUE_CHECKS */;
/*!40101 SET CHARACTER_SET_CLIENT=@OLD_CHARACTER_SET_CLIENT */;
/*!40101 SET CHARACTER_SET_RESULTS=@OLD_CHARACTER_SET_RESULTS */;
/*!40101 SET COLLATION_CONNECTION=@OLD_COLLATION_CONNECTION */;
/*!40111 SET SQL_NOTES=@OLD_SQL_NOTES */;

-- ── 预启动数据 ──

-- 默认 worker
INSERT IGNORE INTO `agent_config` (`pane_id`, `title`, `ttyd_port`, `role`, `workspace`)
VALUES ('w-10001:main.0', 'Main', 10001, 'master', '~/workers/w-10001');

-- worker 编号从 20000 起
INSERT IGNORE INTO `global_vars` (`key_name`, `value`) VALUES ('worker_index', '20000');
