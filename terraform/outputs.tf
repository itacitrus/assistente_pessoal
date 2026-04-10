output "public_ip" {
  description = "Public IP of the bot instance"
  value       = aws_eip.bot.public_ip
}

output "ssh_command" {
  description = "SSH command to connect"
  value       = "ssh -i ~/.ssh/${var.key_name}.pem ec2-user@${aws_eip.bot.public_ip}"
}

output "ssh_tunnel_oauth" {
  description = "SSH tunnel for OAuth callback"
  value       = "ssh -L 8080:localhost:8080 -i ~/.ssh/${var.key_name}.pem ec2-user@${aws_eip.bot.public_ip}"
}
