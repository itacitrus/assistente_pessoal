import type { Metadata } from "next";
import Link from "next/link";

import { LegalDoc } from "@/components/site/LegalDoc";

export const metadata: Metadata = {
  title: "Política de Privacidade — Zello",
  description:
    "Como o Zello, operado pela Itacitrus, coleta, usa, compartilha e protege dados pessoais — incluindo dados de saúde e dados da pessoa cuidada.",
};

export default function PrivacidadePage() {
  return (
    <LegalDoc
      title="Política de Privacidade"
      updated="Última atualização: 21 de maio de 2026"
    >
      <p>
        Esta Política de Privacidade descreve como o <strong>Zello</strong>{" "}
        (&ldquo;nós&rdquo;, &ldquo;serviço&rdquo;), assistente digital operado
        pela <strong>Itacitrus</strong>, coleta, usa, compartilha e protege
        informações pessoais. O Zello funciona pelo WhatsApp e, opcionalmente,
        integra-se ao Google Calendar. Além de organizar a rotina de quem o usa,
        o Zello pode acompanhar o bem-estar e os lembretes de medicação de uma
        pessoa querida — por exemplo, um pai, uma mãe ou um filho. Por isso, esta
        Política trata com cuidado especial os dados de saúde e os dados de
        terceiros.
      </p>
      <p>
        Leia também os nossos{" "}
        <Link href="/termos">Termos de Uso</Link>.
      </p>

      <h2>1. Duas figuras: quem usa e quem é cuidado</h2>
      <p>O Zello pode envolver duas pessoas distintas:</p>
      <ul>
        <li>
          <strong>Titular/usuário (responsável ou cuidador):</strong> quem cria
          a conta, opera o painel e, se quiser, cadastra outra pessoa para
          acompanhar. É também quem aceita estes termos.
        </li>
        <li>
          <strong>Pessoa cuidada (dependente):</strong> a pessoa cujo número de
          WhatsApp o responsável cadastra para receber lembretes e companhia
          (frequentemente um familiar idoso). O Zello conversa diretamente com
          essa pessoa no WhatsApp dela. Os dados dela também são tratados por
          nós, mas com proteções específicas descritas nesta Política.
        </li>
      </ul>
      <p>
        Quando o responsável cadastra um dependente,{" "}
        <strong>
          o Zello envia uma mensagem de boas-vindas ao número da pessoa cuidada
        </strong>
        , identificando-se, explicando que foi adicionado por aquele familiar e
        informando como pedir silêncio ou recusar o acompanhamento. Transparência
        com a pessoa cuidada é um princípio do produto, não uma cortesia.
      </p>

      <h2>2. Informações que coletamos</h2>
      <h3>2.1 Do titular/usuário</h3>
      <ul>
        <li>
          <strong>Número de telefone do WhatsApp</strong> — identifica você e
          recebe suas mensagens.
        </li>
        <li>
          <strong>Nome</strong> — para personalizar a interação.
        </li>
        <li>
          <strong>E-mail e senha da conta no painel</strong> (quando você cria
          conta no site).
        </li>
        <li>
          <strong>E-mail da conta Google e refresh token</strong> — somente se
          você conectar o Google Calendar (opcional). O refresh token é a
          credencial emitida pelo Google após sua autorização explícita, usada
          para acessar seu Calendar em seu nome.
        </li>
        <li>
          <strong>Histórico de mensagens</strong> — para manter contexto da
          conversa.
        </li>
        <li>
          <strong>Transcrições de áudio</strong> — geradas quando você envia
          mensagens de voz.
        </li>
        <li>
          <strong>Preferências</strong> — horário de resumos, fuso horário,
          intervalo de lembretes, prazos de confirmação.
        </li>
        <li>
          <strong>Fatos pessoais que você compartilha</strong> — pessoas do seu
          convívio, viagens, compromissos e detalhes que ajudam o Zello a ser
          útil.
        </li>
      </ul>
      <h3>2.2 Da pessoa cuidada (dependente)</h3>
      <p>
        Quando você cadastra um dependente, podemos tratar, sobre essa pessoa:
      </p>
      <ul>
        <li>
          <strong>Nome, grau de parentesco e número de WhatsApp.</strong>
        </li>
        <li>
          <strong>Medicação:</strong> nomes de medicamentos, doses, instruções,
          horários e <strong>status de adesão</strong> (tomou, não tomou, pulou)
          — dados de saúde.
        </li>
        <li>
          <strong>Síntese diária de bem-estar:</strong> sinais agregados
          inferidos das conversas, como humor, energia, sociabilidade e
          autocuidado — dados de saúde.
        </li>
        <li>
          <strong>Fatos pessoais</strong> relevantes para a companhia (o neto, a
          novela, a horta, o nome do médico) que ajudam o Zello a puxar conversa
          de forma genuína.
        </li>
        <li>
          <strong>Eventos de alerta e escalonamento:</strong> registros de
          quando a família foi avisada (ex.: medicação não confirmada, longo
          período de silêncio, sinal preocupante) e o resultado.
        </li>
        <li>
          <strong>Histórico de mensagens e transcrições de áudio</strong> da
          conversa do dependente com o Zello.
        </li>
      </ul>

      <h2>3. Dados sensíveis de saúde e como os minimizamos</h2>
      <p>
        Medicação e síntese de bem-estar são{" "}
        <strong>dados pessoais sensíveis</strong> (saúde) nos termos do Art. 11
        da LGPD. Tratamos esses dados com cuidado reforçado:
      </p>
      <ul>
        <li>
          Coletamos <strong>apenas o necessário</strong> para lembrar a
          medicação, oferecer companhia e dar tranquilidade à família.
        </li>
        <li>
          A família{" "}
          <strong>nunca recebe o conteúdo literal das conversas</strong> da
          pessoa cuidada. Recebe apenas{" "}
          <strong>sinais agregados de bem-estar</strong> e eventos objetivos
          (ex.: &ldquo;não confirmou o remédio das 8h&rdquo;). Esse limite é
          descrito na seção 5.
        </li>
        <li>
          O Zello <strong>não emite diagnóstico nem opinião clínica</strong>. A
          síntese de bem-estar é uma leitura de sinais de conversa, não uma
          avaliação médica.
        </li>
        <li>
          Sinais classificados internamente como de risco psicológico (memórias
          com categoria de risco) <strong>não são exibidos</strong> no painel da
          família como conteúdo; servem apenas à lógica de cuidado e à decisão de
          alertar.
        </li>
      </ul>

      <h2>4. Bases legais (LGPD)</h2>
      <p>Tratamos dados com fundamento em:</p>
      <ul>
        <li>
          <strong>Consentimento</strong> (Art. 7º, I e Art. 11, I) — você
          consente ao criar a conta e usar o serviço; a pessoa cuidada é
          informada e pode recusar/pedir silêncio a qualquer momento (ver seção
          9).
        </li>
        <li>
          <strong>Execução de contrato e procedimentos preliminares</strong>{" "}
          (Art. 7º, V) — para entregar as funcionalidades que você solicitou.
        </li>
        <li>
          <strong>Tutela da saúde</strong> (Art. 11, II, &ldquo;f&rdquo;) — para
          os lembretes de medicação e sinais de bem-estar, sempre em benefício da
          pessoa cuidada.
        </li>
        <li>
          <strong>Legítimo interesse</strong> (Art. 7º, IX), de forma limitada —
          segurança, prevenção a fraude e operação técnica.
        </li>
      </ul>
      <p>
        <strong>Sobre o cadastro de terceiros:</strong> ao registrar o número de
        uma pessoa cuidada, você{" "}
        <strong>declara possuir autoridade e relação legítima</strong> para
        fazê-lo (parente, cuidador autorizado, responsável legal) e
        compromete-se a informá-la sobre o acompanhamento. A pessoa cuidada
        também é informada diretamente pelo Zello (mensagem de boas-vindas) e
        pode revogar o acompanhamento. O consentimento de cada vínculo é
        registrado e a revogação é definitiva: revogado o consentimento, a
        família deixa de receber qualquer informação sobre aquela pessoa.
      </p>

      <h2>5. O compromisso central: a família vê sinais, não conversas</h2>
      <p>
        Este é o princípio que sustenta a confiança no Zello e nós o tratamos
        como uma obrigação:
      </p>
      <ul>
        <li>
          A pessoa que cuida recebe um{" "}
          <strong>retrato de bem-estar</strong> — humor, energia, autocuidado —
          e <strong>eventos objetivos</strong> (medicação não confirmada,
          inatividade prolongada, sinal preocupante).
        </li>
        <li>
          A pessoa que cuida{" "}
          <strong>não tem acesso ao texto das conversas</strong> da pessoa
          cuidada, nem às transcrições de áudio dela.
        </li>
        <li>
          &ldquo;Cuidar não é vigiar&rdquo;: o Zello insiste com gentileza, nunca
          pressiona, e respeita o pedido de silêncio da pessoa cuidada.
        </li>
      </ul>

      <h2>6. Dados do Google que acessamos (opcional)</h2>
      <p>
        Se — e somente se — você conectar o Google Calendar, com sua autorização
        explícita via tela de consentimento do Google, utilizamos somente o
        escopo:
      </p>
      <ul>
        <li>
          <code>https://www.googleapis.com/auth/calendar.events</code> — permite
          ler, criar, editar e excluir eventos do seu Google Calendar a pedido
          seu.
        </li>
      </ul>
      <p>
        <strong>Não acessamos:</strong> Gmail, Drive, Contatos, Fotos, Maps,
        histórico de localização, ou qualquer outra API do Google fora do escopo
        acima. O Zello funciona sem o Google Calendar — a conexão é opcional e
        habilita apenas as funções de agenda.
      </p>

      <h2>7. Limited Use — Google API Services User Data Policy</h2>
      <blockquote>
        <p>
          O uso e a transferência pelo Zello de informações recebidas das APIs
          do Google a qualquer outro aplicativo aderem à{" "}
          <a
            href="https://developers.google.com/terms/api-services-user-data-policy"
            target="_blank"
            rel="noopener noreferrer"
          >
            Política de Dados do Usuário de Serviços de API do Google
          </a>
          , incluindo os requisitos de <em>Limited Use</em>.
        </p>
      </blockquote>
      <p>Concretamente, isso significa que:</p>
      <ul>
        <li>
          Dados do Google Calendar são usados <strong>exclusivamente</strong>{" "}
          para as funcionalidades descritas nesta política.
        </li>
        <li>Não usamos dados do Google para exibir anúncios.</li>
        <li>Não vendemos dados do Google a terceiros.</li>
        <li>
          Nenhum ser humano lê seus dados do Google, exceto (i) com sua permissão
          específica, (ii) para fins de segurança, (iii) para cumprir obrigações
          legais, ou (iv) de forma agregada e anonimizada para operação interna.
        </li>
        <li>
          Não usamos dados do Google Calendar para treinar modelos de
          inteligência artificial.
        </li>
      </ul>

      <h2>8. Como usamos as informações</h2>
      <ul>
        <li>
          Processar suas solicitações de agenda (criar, consultar, editar ou
          excluir eventos), a pedido seu.
        </li>
        <li>Gerar resumos diários e semanais da sua agenda.</li>
        <li>
          Enviar lembretes de compromissos, de medicação e alertas de conflito.
        </li>
        <li>
          Lembrar a pessoa cuidada de tomar os medicamentos e insistir com
          gentileza até a confirmação.
        </li>
        <li>
          Oferecer companhia conversacional à pessoa cuidada, lembrando do que
          importa para ela.
        </li>
        <li>Inferir e registrar a síntese diária de bem-estar.</li>
        <li>
          Avisar a família, conforme as preferências configuradas, em casos de
          medicação não confirmada, inatividade prolongada ou sinal preocupante.
        </li>
        <li>
          Confirmar ações sensíveis (ex.: excluir um evento) antes de executar.
        </li>
        <li>Manter o histórico de conversa para dar contexto às respostas.</li>
        <li>
          Manter um registro de auditoria das ações relevantes (ex.: avisos
          enviados à família), para segurança e prestação de contas.
        </li>
      </ul>

      <h2>9. Direitos das duas figuras (LGPD)</h2>
      <p>
        Conforme a Lei Geral de Proteção de Dados (Lei 13.709/2018), tanto o
        titular/usuário quanto a pessoa cuidada têm direito a:
      </p>
      <ul>
        <li>
          Confirmação da existência de tratamento e <strong>acesso</strong> aos
          dados;
        </li>
        <li>
          <strong>Correção</strong> de dados incompletos ou desatualizados;
        </li>
        <li>
          <strong>Anonimização, bloqueio ou eliminação</strong> de dados
          desnecessários ou tratados em desconformidade;
        </li>
        <li>
          <strong>Portabilidade</strong>;
        </li>
        <li>
          <strong>Revogação do consentimento</strong> a qualquer momento;
        </li>
        <li>
          <strong>Informação</strong> sobre o compartilhamento dos dados;
        </li>
        <li>
          <strong>Oposição</strong> a tratamento feito com base que não o
          consentimento.
        </li>
      </ul>
      <p>
        <strong>Especificamente para a pessoa cuidada:</strong> ela pode, a
        qualquer momento e diretamente na conversa do WhatsApp,{" "}
        <strong>pedir silêncio</strong> (o Zello para de puxar conversa) ou{" "}
        <strong>recusar o acompanhamento</strong> (revogar o consentimento).
        Revogado o consentimento, a família <strong>deixa de receber</strong>{" "}
        sinais de bem-estar e alertas sobre ela. A pessoa cuidada também pode
        solicitar acesso ou exclusão dos seus dados pelo contato da seção 14, sem
        depender do responsável.
      </p>

      <h2>10. Crianças e adolescentes</h2>
      <p>
        O Zello pode ser usado por um responsável para acompanhar lembretes de um
        filho menor de idade. Quando isso acontece:
      </p>
      <ul>
        <li>
          O cadastro e o consentimento são feitos por quem detém a{" "}
          <strong>responsabilidade parental ou guarda legal</strong>, no melhor
          interesse da criança ou adolescente (Art. 14 da LGPD).
        </li>
        <li>
          Coletamos o mínimo necessário e não direcionamos publicidade a
          menores.
        </li>
        <li>
          A conta titular continua sendo de um adulto responsável; o serviço não
          se destina a ser operado de forma autônoma por menores de 18 anos.
        </li>
      </ul>
      <p>
        Se identificarmos tratamento de dados de menor sem a devida autorização
        parental, removeremos os dados.
      </p>

      <h2>11. Compartilhamento com terceiros (operadores)</h2>
      <p>
        Alguns serviços técnicos processam dados para viabilizar o funcionamento
        do assistente:
      </p>
      <ul>
        <li>
          <strong>Anthropic</strong> (Claude API) — processa o texto das
          mensagens para gerar respostas e a síntese de bem-estar. Segundo a
          política da Anthropic, conteúdo de API não é retido para treinamento. O
          processamento pode ocorrer fora do Brasil.
        </li>
        <li>
          <strong>DeepSeek</strong> — usado como modelo conversacional na camada
          de companhia da pessoa cuidada. O processamento ocorre{" "}
          <strong>fora do Brasil</strong>; trata-se de transferência
          internacional de dados, adotada com as salvaguardas aplicáveis. Caso a
          chave do DeepSeek não esteja configurada, essa função recai sobre a
          Anthropic.
        </li>
        <li>
          <strong>AssemblyAI</strong> — transcreve mensagens de áudio; os dados
          são processados e descartados após a transcrição. O processamento pode
          ocorrer fora do Brasil.
        </li>
        <li>
          <strong>Google</strong> — provedor da API do Calendar (apenas se você
          conectar).
        </li>
        <li>
          <strong>Amazon Web Services</strong> — hospedagem dos servidores, na
          região <code>sa-east-1</code> (São Paulo, Brasil).
        </li>
      </ul>
      <p>
        <strong>Nunca vendemos</strong> nem compartilhamos dados para fins de
        marketing. Transferências internacionais (Anthropic, DeepSeek,
        AssemblyAI) são feitas com base nas hipóteses do Art. 33 da LGPD, com as
        garantias contratuais aplicáveis.
      </p>

      <h2>12. Armazenamento e segurança</h2>
      <ul>
        <li>
          Servidores na AWS, região <code>sa-east-1</code> (São Paulo, Brasil).
        </li>
        <li>
          Refresh tokens do Google e credenciais sensíveis armazenados com
          criptografia <strong>AES-256-GCM</strong> em repouso.
        </li>
        <li>
          Conexões com WhatsApp, Google e processadores sempre via HTTPS/TLS.
        </li>
        <li>
          Backups automáticos do banco de dados a cada 6 horas, retidos por 30
          dias em bucket S3 com acesso restrito.
        </li>
        <li>
          Acesso administrativo restrito a operadores autenticados (chave SSH +
          AWS SSM), com registro de auditoria.
        </li>
      </ul>

      <h2>13. Retenção e exclusão de dados</h2>
      <p>
        Os dados são mantidos enquanto a conta estiver ativa. Ao solicitar
        exclusão, todos os dados — refresh token, histórico de conversas,
        transcrições, preferências, medicação, sínteses de bem-estar e fatos
        pessoais — são apagados dos nossos servidores em até 7 dias. Backups
        históricos expiram conforme o ciclo de retenção de 30 dias.
      </p>
      <p>Você pode solicitar a exclusão a qualquer momento:</p>
      <ul>
        <li>
          Envie um e-mail para{" "}
          <a href="mailto:desenvolvimento@itacitrus.com.br">
            desenvolvimento@itacitrus.com.br
          </a>{" "}
          com o assunto &ldquo;Excluir dados&rdquo;.
        </li>
        <li>
          A pessoa cuidada pode pedir a própria exclusão pelo mesmo e-mail ou
          pela conversa no WhatsApp.
        </li>
        <li>
          Opcionalmente, revogue a autorização do Google em{" "}
          <a
            href="https://myaccount.google.com/permissions"
            target="_blank"
            rel="noopener noreferrer"
          >
            myaccount.google.com/permissions
          </a>{" "}
          — isso invalida o refresh token imediatamente, mas não apaga os demais
          dados armazenados.
        </li>
      </ul>

      <h2>14. Encarregado e contato</h2>
      <p>
        Dúvidas, solicitações ou exercício de direitos (de qualquer das figuras):
      </p>
      <p>
        <a href="mailto:desenvolvimento@itacitrus.com.br">
          desenvolvimento@itacitrus.com.br
        </a>
      </p>

      <h2>15. Alterações nesta Política</h2>
      <p>
        Podemos atualizar esta Política periodicamente. Mudanças materiais serão
        comunicadas via WhatsApp aos usuários ativos, e a data no topo desta
        página sempre refletirá a última atualização.
      </p>
    </LegalDoc>
  );
}
