package services

type TelegramBot struct {
}

func NewTelegramBot() *TelegramBot {
	return &TelegramBot{}
}

func (b *TelegramBot) SendFile(path string) error {
	return nil
}
