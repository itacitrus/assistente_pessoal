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
  default     = "https://github.com/itacitrus/assistente_pessoal.git"
}

variable "data_volume_size" {
  description = "Size in GB of the persistent data EBS volume"
  type        = number
  default     = 10
}

variable "backup_bucket_name" {
  description = "S3 bucket name for database backups"
  type        = string
  default     = "assistente-backups"
}
