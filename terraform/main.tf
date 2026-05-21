terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.aws_region
}

data "aws_ami" "amazon_linux" {
  most_recent = true
  owners      = ["amazon"]

  filter {
    name   = "name"
    values = ["al2023-ami-*-x86_64"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

data "aws_availability_zones" "available" {
  state = "available"
}

resource "aws_security_group" "bot" {
  name_prefix = "assistente-bot-"
  description = "Security group for WhatsApp bot"

  ingress {
    description = "SSH from admin"
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = [var.admin_ip]
  }

  ingress {
    description     = "OAuth callback + health check from shared ALB"
    from_port       = 8080
    to_port         = 8080
    protocol        = "tcp"
    security_groups = [tolist(data.aws_lb.api.security_groups)[0]]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "assistente-bot"
  }
}

resource "aws_eip" "bot" {
  domain = "vpc"
  tags = {
    Name = "assistente-bot"
  }
}

# S3 bucket for automated DB backups. Versioning enabled so an accidental
# delete can be recovered; public access blocked.
resource "aws_s3_bucket" "backups" {
  bucket = var.backup_bucket_name

  tags = {
    Name = "assistente-bot-backups"
  }
}

resource "aws_s3_bucket_versioning" "backups" {
  bucket = aws_s3_bucket.backups.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_public_access_block" "backups" {
  bucket                  = aws_s3_bucket.backups.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_lifecycle_configuration" "backups" {
  bucket = aws_s3_bucket.backups.id

  rule {
    id     = "expire-old-backups"
    status = "Enabled"

    filter {
      prefix = "backups/"
    }

    expiration {
      days = 30
    }

    noncurrent_version_expiration {
      noncurrent_days = 14
    }
  }
}

# IAM role + instance profile so the bot can upload backups to S3 without
# baking credentials into .env or the AMI.
resource "aws_iam_role" "bot" {
  name = "assistente-bot"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        Service = "ec2.amazonaws.com"
      }
      Action = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "bot_s3_backup" {
  name = "s3-backup-access"
  role = aws_iam_role.bot.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "s3:PutObject",
          "s3:GetObject",
          "s3:ListBucket",
        ]
        Resource = [
          aws_s3_bucket.backups.arn,
          "${aws_s3_bucket.backups.arn}/*",
        ]
      },
    ]
  })
}

resource "aws_iam_instance_profile" "bot" {
  name = "assistente-bot"
  role = aws_iam_role.bot.name
}

# Attach the AWS-managed SSM policy so we can open shell sessions via
# Session Manager (no SSH port, no IP allowlist — outbound only).
resource "aws_iam_role_policy_attachment" "bot_ssm" {
  role       = aws_iam_role.bot.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

# Persistent data volume. Kept separate from the root so the SQLite DBs
# survive instance recreation.
resource "aws_ebs_volume" "data" {
  availability_zone = data.aws_availability_zones.available.names[1] # sa-east-1b
  size              = var.data_volume_size
  type              = "gp3"
  encrypted         = true

  tags = {
    Name = "assistente-bot-data"
  }

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_volume_attachment" "data" {
  device_name = "/dev/sdf"
  volume_id   = aws_ebs_volume.data.id
  instance_id = aws_instance.bot.id

  # If the instance is destroyed, detach cleanly — the volume survives.
  stop_instance_before_detaching = true
}

resource "aws_instance" "bot" {
  ami                    = data.aws_ami.amazon_linux.id
  instance_type          = var.instance_type
  key_name               = var.key_name
  vpc_security_group_ids = [aws_security_group.bot.id]
  iam_instance_profile   = aws_iam_instance_profile.bot.name
  availability_zone      = data.aws_availability_zones.available.names[1] # matches data volume AZ

  root_block_device {
    volume_size           = 20
    volume_type           = "gp3"
    delete_on_termination = true
  }

  user_data = templatefile("${path.module}/cloud-init.yaml", {
    repo_url       = var.repo_url
    backup_bucket  = var.backup_bucket_name
    data_device    = "/dev/sdf"
  })

  tags = {
    Name = "assistente-bot"
  }
}

resource "aws_eip_association" "bot" {
  instance_id   = aws_instance.bot.id
  allocation_id = aws_eip.bot.id
}

# -----------------------------------------------------------------------------
# Shared ALB integration
#
# We reuse the existing api-sankhya-lb instead of provisioning a new LB just for
# the bot's OAuth callback — it's low-traffic (one hit per onboarding) and the
# LB already sits in the same VPC with a valid ACM cert. We attach a host-path
# rule on the :443 listener that routes /assistente/oauth/callback to a new
# target group pointing at the bot EC2 on :8080.
# -----------------------------------------------------------------------------

data "aws_lb" "api" {
  name = var.shared_alb_name
}

data "aws_lb_listener" "api_https" {
  load_balancer_arn = data.aws_lb.api.arn
  port              = 443
}

resource "aws_lb_target_group" "bot" {
  name        = "assistente-bot-tg"
  port        = 8080
  protocol    = "HTTP"
  target_type = "instance"
  vpc_id      = data.aws_lb.api.vpc_id

  health_check {
    enabled             = true
    path                = "/health"
    port                = "traffic-port"
    protocol            = "HTTP"
    matcher             = "200"
    healthy_threshold   = 2
    unhealthy_threshold = 3
    interval            = 30
    timeout             = 5
  }

  tags = {
    Name = "assistente-bot"
  }
}

resource "aws_lb_target_group_attachment" "bot" {
  target_group_arn = aws_lb_target_group.bot.arn
  target_id        = aws_instance.bot.id
  port             = 8080
}

resource "aws_lb_listener_rule" "bot_oauth" {
  listener_arn = data.aws_lb_listener.api_https.arn
  priority     = 100

  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.bot.arn
  }

  condition {
    path_pattern {
      values = ["/assistente/oauth/callback"]
    }
  }

  tags = {
    Name = "assistente-bot-oauth"
  }
}

# Painel web (Fase 2 — assistente de idosos). O ALB compartilhado roteia por
# default tudo pro serviço Sankhya; esta regra (prioridade 90, mais específica
# que a default e abaixo da regra OAuth) manda /assistente/api/v1/* pro bot.
# O bot monta a API REST sob esse prefixo via env API_PATH_PREFIX=/assistente.
# Criada manualmente via CLI em 2026-05-20 e codificada aqui pra eliminar drift
# (terraform import: aws_lb_listener_rule.bot_api <rule-arn>).
resource "aws_lb_listener_rule" "bot_api" {
  listener_arn = data.aws_lb_listener.api_https.arn
  priority     = 90

  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.bot.arn
  }

  condition {
    path_pattern {
      values = ["/assistente/api/v1/*"]
    }
  }

  tags = {
    Name = "assistente-bot-api"
  }
}
