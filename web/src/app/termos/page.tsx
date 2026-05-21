import type { Metadata } from "next";
import Link from "next/link";

import { LegalDoc } from "@/components/site/LegalDoc";

export const metadata: Metadata = {
  title: "Termos de Uso — Zello",
  description:
    "Condições para uso do Zello, assistente da Itacitrus no WhatsApp — incluindo responsabilidades de quem cuida e o aviso de que o Zello não é um serviço médico.",
};

export default function TermosPage() {
  return (
    <LegalDoc
      title="Termos de Uso"
      updated="Última atualização: 21 de maio de 2026"
    >
      <p>
        Ao utilizar o <strong>Zello</strong> (&ldquo;serviço&rdquo;), assistente
        digital operado pela <strong>Itacitrus</strong>, você concorda com estes
        Termos de Uso. Se não concordar com qualquer parte, não utilize o
        serviço. O tratamento de dados é descrito na{" "}
        <Link href="/privacidade">Política de Privacidade</Link>.
      </p>

      <h2>1. Descrição do serviço</h2>
      <p>
        O Zello é um assistente conversacional acessado pelo WhatsApp que ajuda
        você a organizar a rotina e a cuidar de quem você ama. Ele pode:
      </p>
      <ul>
        <li>
          Integrar-se, <strong>opcionalmente</strong>, ao Google Calendar para
          gerenciar eventos, lembretes e períodos de viagem em linguagem
          natural.
        </li>
        <li>
          Lembrar uma <strong>pessoa cuidada</strong> (ex.: um familiar idoso ou
          um filho) de tomar medicamentos, oferecer companhia conversacional e
          dar à família um retrato do bem-estar dessa pessoa.
        </li>
      </ul>
      <p>
        O Zello conversa com cada pessoa na própria conversa de WhatsApp dela. A
        família recebe <strong>sinais agregados de bem-estar e eventos</strong>,
        nunca o conteúdo das conversas da pessoa cuidada.
      </p>

      <h2>2. O que o Zello NÃO é — aviso importante</h2>
      <p>
        <strong>
          O Zello não é um dispositivo médico, não é serviço de saúde e não é
          serviço de emergência.
        </strong>{" "}
        Em especial:
      </p>
      <ul>
        <li>
          É um assistente de <strong>lembretes e companhia</strong>; não
          diagnostica, não prescreve, não monitora sinais vitais e não substitui
          o julgamento de médicos, enfermeiros, cuidadores profissionais ou
          serviços de urgência.
        </li>
        <li>
          Os lembretes de medicação e os sinais de bem-estar são{" "}
          <strong>auxílios</strong>, não garantias.{" "}
          <strong>
            Não confie no Zello para situações críticas de saúde ou emergências
          </strong>{" "}
          — em emergência, ligue para o SAMU (192), o serviço médico ou os
          contatos de emergência adequados.
        </li>
        <li>
          Os alertas à família (medicação não confirmada, inatividade, sinal
          preocupante) são enviados conforme melhor esforço e dependem de
          serviços de terceiros; podem atrasar ou falhar. Assim como na agenda —{" "}
          <strong>
            use o Google Calendar diretamente para verificar compromissos em
            situações críticas
          </strong>{" "}
          — para saúde,{" "}
          <strong>
            mantenha o acompanhamento profissional e os cuidados habituais
          </strong>
          , independentemente do Zello.
        </li>
      </ul>

      <h2>3. Elegibilidade</h2>
      <ul>
        <li>
          Você precisa ter ao menos <strong>18 anos</strong>.
        </li>
        <li>
          Você precisa ser titular legítimo do número de WhatsApp utilizado na
          conta.
        </li>
        <li>
          Se conectar o Google Calendar, precisa possuir conta Google válida e
          autorizar explicitamente o acesso (passo opcional).
        </li>
      </ul>

      <h2>4. Cadastro e acesso</h2>
      <p>
        O cadastro é <strong>aberto</strong>: você cria sua conta diretamente no
        site, informando seu WhatsApp e seus dados básicos. A conexão com o
        Google Calendar, quando desejada, é feita por você na tela oficial do
        Google. (O serviço já não opera em beta fechado por convite.)
      </p>

      <h2>5. Responsabilidades de quem cuida (cadastro de terceiros)</h2>
      <p>
        Ao cadastrar uma <strong>pessoa cuidada</strong> (dependente), você
        declara e garante que:
      </p>
      <ul>
        <li>
          Possui <strong>relação legítima e autoridade</strong> para fazê-lo — é
          familiar, cuidador autorizado, responsável legal ou detentor de
          guarda/responsabilidade parental, conforme o caso.
        </li>
        <li>
          <strong>Informará a pessoa cuidada</strong> sobre o acompanhamento e
          respeitará a vontade dela, inclusive o pedido de silêncio ou de não
          participar. (O Zello também a informa diretamente por mensagem de
          boas-vindas.)
        </li>
        <li>
          Tem <strong>base legal e consentimento adequados</strong> para que
          sejam tratados os dados dessa pessoa, incluindo dados de saúde
          (medicação e bem-estar), e para receber os sinais agregados sobre ela.
        </li>
        <li>
          Cumprirá a LGPD na sua parcela de responsabilidade. A conformidade é{" "}
          <strong>compartilhada</strong>: a Itacitrus atua como
          operadora/controladora dos meios técnicos, mas a legitimidade do
          vínculo com a pessoa cuidada depende de você.
        </li>
      </ul>
      <p>
        Você responde por cadastros indevidos de terceiros e nos isentará de
        reclamações decorrentes de cadastro sem autoridade ou consentimento.
      </p>

      <h2>6. Uso aceitável</h2>
      <p>
        Você concorda em <strong>não</strong>:
      </p>
      <ul>
        <li>Usar o serviço para qualquer atividade ilegal ou não autorizada.</li>
        <li>
          Cadastrar terceiros sem autoridade, relação legítima ou ciência da
          pessoa.
        </li>
        <li>
          Usar os sinais de bem-estar para vigilância abusiva, coação ou qualquer
          finalidade que viole a dignidade ou a privacidade da pessoa cuidada.
        </li>
        <li>
          Tentar comprometer a segurança, integridade ou disponibilidade do
          sistema ou de outros usuários.
        </li>
        <li>
          Realizar engenharia reversa, descompilação ou tentativa de extrair
          código-fonte.
        </li>
        <li>
          Enviar volume de mensagens que caracterize abuso ou sobrecarga da
          infraestrutura.
        </li>
        <li>
          Transmitir conteúdo ilegal, ofensivo, difamatório ou que viole direitos
          de terceiros.
        </li>
        <li>
          Utilizar o serviço para spam ou comunicação não solicitada a terceiros.
        </li>
      </ul>

      <h2>7. Sua conta e seus dados</h2>
      <p>
        Você mantém a propriedade dos dados que envia ou que são criados por sua
        solicitação (eventos, mensagens, conteúdos). Nós apenas os processamos
        conforme a <Link href="/privacidade">Política de Privacidade</Link>. Você
        é responsável por manter a confidencialidade do acesso às suas contas
        (WhatsApp, painel e Google) e por qualquer ação realizada por meio delas.
      </p>

      <h2>8. Disponibilidade</h2>
      <p>
        O serviço é fornecido &ldquo;como está&rdquo; e &ldquo;conforme
        disponível&rdquo;, sem garantias expressas ou implícitas. Não garantimos
        uptime contínuo, ausência de erros ou disponibilidade em janelas
        específicas. Manutenções, atualizações, dependências externas (WhatsApp,
        Google e demais provedores dos quais o serviço depende) ou incidentes
        técnicos podem causar indisponibilidade temporária — inclusive de
        lembretes e alertas.
      </p>

      <h2>9. Propriedade intelectual</h2>
      <p>
        O código-fonte, a marca &ldquo;Zello&rdquo;, a marca
        &ldquo;Itacitrus&rdquo;, o design e todos os conteúdos proprietários do
        serviço pertencem à Itacitrus ou a seus licenciadores. Nenhum direito
        além do uso do serviço nos termos deste documento é concedido a você.
      </p>

      <h2>10. Limitação de responsabilidade</h2>
      <p>
        Na máxima extensão permitida por lei, a Itacitrus não se responsabiliza
        por:
      </p>
      <ul>
        <li>
          Danos indiretos, incidentais, consequenciais, lucros cessantes ou perda
          de oportunidade;
        </li>
        <li>
          Indisponibilidade de serviços de terceiros (WhatsApp, Google e demais
          provedores externos) que afetem o funcionamento do serviço;
        </li>
        <li>
          Perdas decorrentes de eventos não criados, não excluídos ou não
          notificados por falha técnica —{" "}
          <strong>
            use o Google Calendar diretamente para verificar sua agenda em
            situações críticas
          </strong>
          ;
        </li>
        <li>
          Consequências de saúde decorrentes de lembrete de medicação não
          entregue ou atrasado, de alerta não recebido pela família ou de sinal
          de bem-estar interpretado equivocadamente —{" "}
          <strong>
            o Zello não substitui acompanhamento médico nem cuidado profissional
          </strong>
          , conforme a seção 2.
        </li>
      </ul>

      <h2>11. Rescisão</h2>
      <p>
        Você pode parar de usar o serviço a qualquer momento solicitando a
        exclusão dos seus dados conforme a{" "}
        <Link href="/privacidade">Política de Privacidade</Link>. A pessoa cuidada
        pode revogar o acompanhamento a qualquer momento. Podemos suspender ou
        encerrar o acesso em caso de violação destes Termos, atividade
        fraudulenta, cadastro indevido de terceiros, ou por decisão de
        descontinuar o serviço — neste último caso, com aviso prévio razoável.
      </p>

      <h2>12. Alterações destes Termos</h2>
      <p>
        Podemos atualizar estes Termos periodicamente. Mudanças materiais serão
        comunicadas via WhatsApp aos usuários ativos. O uso continuado após a
        comunicação constitui aceitação dos novos Termos.
      </p>

      <h2>13. Lei aplicável e foro</h2>
      <p>
        Estes Termos são regidos pelas leis da República Federativa do Brasil.
        Fica eleito o foro da comarca de <strong>Curitiba/PR</strong> como
        competente para dirimir quaisquer controvérsias, com renúncia expressa a
        qualquer outro, por mais privilegiado que seja.
      </p>

      <h2>14. Contato</h2>
      <p>
        Para dúvidas, solicitações ou comunicações relativas a estes Termos:
      </p>
      <p>
        <a href="mailto:desenvolvimento@itacitrus.com.br">
          desenvolvimento@itacitrus.com.br
        </a>
      </p>
    </LegalDoc>
  );
}
