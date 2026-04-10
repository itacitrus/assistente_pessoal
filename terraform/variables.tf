variable "aws_region" {
  description = "AWS region"
  type        = string
  default     = "sa-east-1"
}

variable "instance_type" {
  description = "EC2 instance type"
  type        = string
  default     = "t3.small"
}

variable "admin_ip" {
  description = "Admin IP for SSH access (CIDR, e.g. 1.2.3.4/32)"
  type        = string
}

variable "key_name" {
  description = "Name of existing EC2 Key Pair for SSH"
  type        = string
}

variable "repo_url" {
  description = "Git repository URL to clone"
  type        = string
  default     = "https://github.com/giovannirambo/assistente_pessoal.git"
}
